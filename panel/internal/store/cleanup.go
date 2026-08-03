package store

import (
	"fmt"
	"time"
)

// Retention policy for PruneExpiredData.
const (
	// TaskRetentionDays bounds how long finished task rows are kept.
	TaskRetentionDays = 30
	// AuditRetentionDays bounds pki_audit_logs / automation_audit_logs retention.
	AuditRetentionDays = 90
	// SnapshotsPerNode bounds config_snapshots kept per node.
	SnapshotsPerNode = 20
)

// PruneSummary reports how many rows each retention pass deleted.
type PruneSummary struct {
	Tasks               int64
	Snapshots           int64
	PKIAuditLogs        int64
	AutomationAuditLogs int64
	EnrollmentTokens    int64
}

// Total returns the sum of all deleted rows.
func (p PruneSummary) Total() int64 {
	return p.Tasks + p.Snapshots + p.PKIAuditLogs + p.AutomationAuditLogs + p.EnrollmentTokens
}

// PruneExpiredData deletes rows past their retention window: tasks older than
// 30 days, config snapshots beyond the newest 20 per node, audit logs older
// than 90 days, and used or expired PKI enrollment tokens.
func (s *Store) PruneExpiredData(now time.Time) (PruneSummary, error) {
	var summary PruneSummary
	taskCutoff := now.AddDate(0, 0, -TaskRetentionDays).Unix()
	auditCutoff := now.AddDate(0, 0, -AuditRetentionDays).Unix()
	nowUnix := now.Unix()

	steps := []struct {
		stmt string
		args []any
		dest *int64
		name string
	}{
		{`DELETE FROM tasks WHERE created_at_unix < ?`, []any{taskCutoff}, &summary.Tasks, "任务"},
		{`DELETE FROM pki_audit_logs WHERE created_at_unix < ?`, []any{auditCutoff}, &summary.PKIAuditLogs, "PKI 审计日志"},
		{`DELETE FROM automation_audit_logs WHERE created_at_unix < ?`, []any{auditCutoff}, &summary.AutomationAuditLogs, "自动化审计日志"},
		{`DELETE FROM pki_enrollment_tokens WHERE used_at_unix != 0 OR expires_at_unix < ?`, []any{nowUnix}, &summary.EnrollmentTokens, "注册令牌"},
	}
	for _, step := range steps {
		res, err := s.db.Exec(step.stmt, step.args...)
		if err != nil {
			return summary, fmt.Errorf("清理%s失败：%w", step.name, err)
		}
		*step.dest, _ = res.RowsAffected()
	}

	res, err := s.db.Exec(`
		DELETE FROM config_snapshots WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY node_id ORDER BY created_at_unix DESC, id DESC
				) AS rn FROM config_snapshots
			) WHERE rn > ?
		)`, SnapshotsPerNode)
	if err != nil {
		return summary, fmt.Errorf("清理配置快照失败：%w", err)
	}
	summary.Snapshots, _ = res.RowsAffected()
	return summary, nil
}
