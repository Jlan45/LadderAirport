package store

import (
	"database/sql"
	"fmt"
)

// --- Route plans ---

const routePlanSelectCols = `id, name, scope, COALESCE(subscription_id, ''), enabled, sort_order, created_at_unix, updated_at_unix`

func scanRoutePlan(row interface {
	Scan(dest ...any) error
}) (*RoutePlan, error) {
	var p RoutePlan
	var enabled int
	err := row.Scan(
		&p.ID, &p.Name, &p.Scope, &p.SubscriptionID,
		&enabled, &p.SortOrder, &p.CreatedAtUnix, &p.UpdatedAtUnix,
	)
	if err != nil {
		return nil, err
	}
	p.Enabled = enabled != 0
	p.Rules = []RoutePlanRule{}
	return &p, nil
}

// loadRoutePlanRules attaches enabled-and-disabled rules ordered by position.
func (s *Store) loadRoutePlanRules(p *RoutePlan) error {
	rows, err := s.db.Query(`
		SELECT plan_id, position, match_type, match_value, action,
			COALESCE(target_chain_id, ''), enabled
		FROM route_plan_rules WHERE plan_id = ? ORDER BY position`, p.ID)
	if err != nil {
		return fmt.Errorf("加载路由计划规则失败：%w", err)
	}
	defer rows.Close()
	p.Rules = []RoutePlanRule{}
	for rows.Next() {
		var rule RoutePlanRule
		var enabled int
		if err := rows.Scan(
			&rule.PlanID, &rule.Position, &rule.MatchType, &rule.MatchValue,
			&rule.Action, &rule.TargetChainID, &enabled,
		); err != nil {
			return fmt.Errorf("读取路由计划规则失败：%w", err)
		}
		rule.Enabled = enabled != 0
		p.Rules = append(p.Rules, rule)
	}
	return rows.Err()
}

func (s *Store) GetRoutePlan(id string) (*RoutePlan, error) {
	p, err := scanRoutePlan(s.db.QueryRow(
		`SELECT `+routePlanSelectCols+` FROM route_plans WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("路由计划不存在：%s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("读取路由计划失败：%w", err)
	}
	if err := s.loadRoutePlanRules(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ListRoutePlans returns all plans (rules attached), ordered by sort_order
// then creation time.
func (s *Store) ListRoutePlans() ([]RoutePlan, error) {
	rows, err := s.db.Query(`SELECT ` + routePlanSelectCols + `
		FROM route_plans ORDER BY sort_order ASC, created_at_unix ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询路由计划列表失败：%w", err)
	}
	defer rows.Close()
	var out []RoutePlan
	for rows.Next() {
		p, err := scanRoutePlan(rows)
		if err != nil {
			return nil, fmt.Errorf("读取路由计划列表失败：%w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []RoutePlan{}
	}
	for i := range out {
		if err := s.loadRoutePlanRules(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListEnabledGlobalRoutePlans returns enabled global-scope plans with only
// their enabled rules, in effective evaluation order.
func (s *Store) ListEnabledGlobalRoutePlans() ([]RoutePlan, error) {
	rows, err := s.db.Query(`SELECT ` + routePlanSelectCols + `
		FROM route_plans WHERE enabled = 1 AND scope = 'global'
		ORDER BY sort_order ASC, created_at_unix ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询全局路由计划失败：%w", err)
	}
	defer rows.Close()
	var out []RoutePlan
	for rows.Next() {
		p, err := scanRoutePlan(rows)
		if err != nil {
			return nil, fmt.Errorf("读取全局路由计划失败：%w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadRoutePlanRules(&out[i]); err != nil {
			return nil, err
		}
		enabled := out[i].Rules[:0]
		for _, rule := range out[i].Rules {
			if rule.Enabled {
				enabled = append(enabled, rule)
			}
		}
		out[i].Rules = enabled
	}
	return out, nil
}

// CreateRoutePlan inserts the plan and its rules atomically. Rule positions
// are rewritten sequentially following the slice order.
func (s *Store) CreateRoutePlan(p *RoutePlan) error {
	if p == nil {
		return fmt.Errorf("路由计划不能为空")
	}
	if p.ID == "" {
		p.ID = newID()
	}
	now := nowUnix()
	if p.CreatedAtUnix == 0 {
		p.CreatedAtUnix = now
	}
	p.UpdatedAtUnix = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO route_plans
			(id, name, scope, subscription_id, enabled, sort_order, created_at_unix, updated_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Scope, nullableID(p.SubscriptionID), boolToInt(p.Enabled),
		p.SortOrder, p.CreatedAtUnix, p.UpdatedAtUnix); err != nil {
		return fmt.Errorf("创建路由计划失败：%w", err)
	}
	if err := insertRoutePlanRules(tx, p.ID, p.Rules); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateRoutePlan replaces scalar plan fields; rules are managed separately
// via ReplaceRoutePlanRules.
func (s *Store) UpdateRoutePlan(p *RoutePlan) error {
	if p == nil || p.ID == "" {
		return fmt.Errorf("必须提供路由计划 ID")
	}
	p.UpdatedAtUnix = nowUnix()
	res, err := s.db.Exec(`
		UPDATE route_plans SET
			name = ?, scope = ?, subscription_id = ?, enabled = ?, sort_order = ?,
			updated_at_unix = ?
		WHERE id = ?`,
		p.Name, p.Scope, nullableID(p.SubscriptionID), boolToInt(p.Enabled),
		p.SortOrder, p.UpdatedAtUnix, p.ID)
	if err != nil {
		return fmt.Errorf("更新路由计划失败：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("路由计划不存在：%s", p.ID)
	}
	return nil
}

func (s *Store) DeleteRoutePlan(id string) error {
	res, err := s.db.Exec(`DELETE FROM route_plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除路由计划失败：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("路由计划不存在：%s", id)
	}
	return nil
}

// ReplaceRoutePlanRules atomically replaces all rules of a plan, resequencing
// positions to 0..n-1 following the slice order.
func (s *Store) ReplaceRoutePlanRules(planID string, rules []RoutePlanRule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE route_plans SET updated_at_unix = ? WHERE id = ?`, nowUnix(), planID)
	if err != nil {
		return fmt.Errorf("更新路由计划失败：%w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("路由计划不存在：%s", planID)
	}
	if _, err := tx.Exec(`DELETE FROM route_plan_rules WHERE plan_id = ?`, planID); err != nil {
		return fmt.Errorf("清空路由计划规则失败：%w", err)
	}
	if err := insertRoutePlanRules(tx, planID, rules); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRoutePlanRules(tx *sql.Tx, planID string, rules []RoutePlanRule) error {
	for i, rule := range rules {
		if _, err := tx.Exec(`
			INSERT INTO route_plan_rules
				(plan_id, position, match_type, match_value, action, target_chain_id, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			planID, i, rule.MatchType, rule.MatchValue, rule.Action,
			nullableID(rule.TargetChainID), boolToInt(rule.Enabled)); err != nil {
			return fmt.Errorf("保存路由计划第 %d 条规则失败：%w", i+1, err)
		}
	}
	return nil
}

// nullableID maps an empty reference to NULL so SQLite foreign keys (which
// reject ”) are only enforced for real references.
func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
