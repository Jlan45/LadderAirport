package store

import (
	"database/sql"
	"fmt"
	"time"
)

const automationJobCols = `id, type, target_type, target_id, state, payload_json,
	attempt, next_run_unix, lease_owner, lease_expires_unix, last_error,
	created_at_unix, updated_at_unix`

func scanAutomationJob(row interface{ Scan(...any) error }) (*AutomationJob, error) {
	var job AutomationJob
	var payloadJSON string
	if err := row.Scan(
		&job.ID, &job.Type, &job.TargetType, &job.TargetID, &job.State,
		&payloadJSON, &job.Attempt, &job.NextRunUnix, &job.LeaseOwner,
		&job.LeaseExpiresUnix, &job.LastError, &job.CreatedAtUnix,
		&job.UpdatedAtUnix,
	); err != nil {
		return nil, err
	}
	job.Payload = map[string]any{}
	if err := unmarshalJSON(payloadJSON, &job.Payload); err != nil {
		return nil, fmt.Errorf("解析自动化任务参数失败：%w", err)
	}
	return &job, nil
}

// EnqueueAutomationJob returns an existing active job for the same target or
// creates a new pending job.
func (s *Store) EnqueueAutomationJob(job *AutomationJob) (*AutomationJob, error) {
	if job == nil || job.Type == "" || job.TargetType == "" || job.TargetID == "" {
		return nil, fmt.Errorf("必须提供任务类型和目标")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := scanAutomationJob(tx.QueryRow(`
		SELECT `+automationJobCols+` FROM automation_jobs
		WHERE type = ? AND target_type = ? AND target_id = ?
			AND state IN ('pending', 'running', 'retry_wait')
		ORDER BY created_at_unix DESC LIMIT 1`,
		job.Type, job.TargetType, job.TargetID,
	))
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("查询现有自动化任务失败：%w", err)
	}
	payloadJSON, err := marshalJSON(job.Payload)
	if err != nil {
		return nil, fmt.Errorf("编码自动化任务参数失败：%w", err)
	}
	if job.ID == "" {
		job.ID = newID()
	}
	if job.State == "" {
		job.State = "pending"
	}
	now := nowUnix()
	if job.NextRunUnix == 0 {
		job.NextRunUnix = now
	}
	job.CreatedAtUnix = now
	job.UpdatedAtUnix = now
	if _, err := tx.Exec(`
		INSERT INTO automation_jobs (
			id, type, target_type, target_id, state, payload_json, attempt,
			next_run_unix, lease_owner, lease_expires_unix, last_error,
			created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Type, job.TargetType, job.TargetID, job.State, payloadJSON,
		job.Attempt, job.NextRunUnix, job.LeaseOwner, job.LeaseExpiresUnix,
		job.LastError, now, now,
	); err != nil {
		return nil, fmt.Errorf("创建自动化任务失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Store) GetAutomationJob(id string) (*AutomationJob, error) {
	job, err := scanAutomationJob(s.db.QueryRow(
		`SELECT `+automationJobCols+` FROM automation_jobs WHERE id = ?`, id,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("自动化任务不存在：%s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("读取自动化任务失败：%w", err)
	}
	return job, nil
}

func (s *Store) ListAutomationJobs(limit int) ([]AutomationJob, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+automationJobCols+`
		FROM automation_jobs ORDER BY created_at_unix DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询自动化任务失败：%w", err)
	}
	defer rows.Close()
	out := []AutomationJob{}
	for rows.Next() {
		job, err := scanAutomationJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

// ClaimAutomationJobs leases due jobs using SQLite's single-writer transaction.
func (s *Store) ClaimAutomationJobs(owner string, now time.Time, lease time.Duration, limit int) ([]AutomationJob, error) {
	if owner == "" {
		return nil, fmt.Errorf("必须提供任务租约所有者")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`
		SELECT `+automationJobCols+` FROM automation_jobs
		WHERE (
				state IN ('pending', 'retry_wait')
				AND next_run_unix <= ?
				AND (lease_owner = '' OR lease_expires_unix <= ?)
			) OR (
				state = 'running' AND lease_expires_unix <= ?
			)
		ORDER BY next_run_unix, created_at_unix LIMIT ?`,
		now.Unix(), now.Unix(), now.Unix(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("查询到期自动化任务失败：%w", err)
	}
	candidates := []AutomationJob{}
	for rows.Next() {
		job, scanErr := scanAutomationJob(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		candidates = append(candidates, *job)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	jobs := make([]AutomationJob, 0, len(candidates))
	for i := range candidates {
		res, err := tx.Exec(`
			UPDATE automation_jobs SET state = 'running', lease_owner = ?,
				lease_expires_unix = ?, attempt = attempt + 1, updated_at_unix = ?
			WHERE id = ? AND (
				(state IN ('pending', 'retry_wait')
					AND (lease_owner = '' OR lease_expires_unix <= ?))
				OR (state = 'running' AND lease_expires_unix <= ?)
			)`,
			owner, now.Add(lease).Unix(), now.Unix(), candidates[i].ID, now.Unix(),
			now.Unix(),
		)
		if err != nil {
			return nil, fmt.Errorf("领取自动化任务失败：%w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		candidates[i].State = "running"
		candidates[i].LeaseOwner = owner
		candidates[i].LeaseExpiresUnix = now.Add(lease).Unix()
		candidates[i].Attempt++
		jobs = append(jobs, candidates[i])
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) FinishAutomationJob(id, owner, state, message string, nextRunUnix int64) error {
	switch state {
	case "success", "failed", "retry_wait", "cancelled":
	default:
		return fmt.Errorf("自动化任务终态无效：%s", state)
	}
	res, err := s.db.Exec(`
		UPDATE automation_jobs SET state = ?, next_run_unix = ?,
			lease_owner = '', lease_expires_unix = 0, last_error = ?,
			updated_at_unix = ?
		WHERE id = ? AND lease_owner = ?`,
		state, nextRunUnix, message, nowUnix(), id, owner,
	)
	if err != nil {
		return fmt.Errorf("完成自动化任务失败：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("自动化任务租约已失效：%s", id)
	}
	return nil
}

func (s *Store) RenewAutomationJobLease(id, owner string, now time.Time, lease time.Duration) error {
	if lease <= 0 {
		return fmt.Errorf("任务租约时长必须大于零")
	}
	res, err := s.db.Exec(`
		UPDATE automation_jobs SET lease_expires_unix = ?, updated_at_unix = ?
		WHERE id = ? AND state = 'running' AND lease_owner = ?`,
		now.Add(lease).Unix(), now.Unix(), id, owner,
	)
	if err != nil {
		return fmt.Errorf("续期自动化任务租约失败：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("自动化任务租约已失效：%s", id)
	}
	return nil
}

func (s *Store) AddAutomationAudit(log *AutomationAuditLog) error {
	if log == nil || log.Action == "" {
		return fmt.Errorf("必须提供自动化审计动作")
	}
	if log.ID == "" {
		log.ID = newID()
	}
	if log.CreatedAtUnix == 0 {
		log.CreatedAtUnix = nowUnix()
	}
	_, err := s.db.Exec(`
		INSERT INTO automation_audit_logs (
			id, action, target_type, target_id, actor, outcome, detail, created_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		log.ID, log.Action, log.TargetType, log.TargetID, log.Actor,
		log.Outcome, log.Detail, log.CreatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("写入自动化审计日志失败：%w", err)
	}
	return nil
}

func (s *Store) ListAutomationAudits(limit int) ([]AutomationAuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, action, target_type, target_id, actor, outcome, detail, created_at_unix
		FROM automation_audit_logs ORDER BY created_at_unix DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询自动化审计日志失败：%w", err)
	}
	defer rows.Close()
	out := []AutomationAuditLog{}
	for rows.Next() {
		var log AutomationAuditLog
		if err := rows.Scan(
			&log.ID, &log.Action, &log.TargetType, &log.TargetID,
			&log.Actor, &log.Outcome, &log.Detail, &log.CreatedAtUnix,
		); err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}
