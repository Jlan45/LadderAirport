package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// CreatePKIEnrollmentToken creates a short-lived one-time secret. Only its
// SHA-256 digest is persisted.
func (s *Store) CreatePKIEnrollmentToken(nodeID string, ttl time.Duration) (string, error) {
	if nodeID == "" {
		return "", fmt.Errorf("必须提供节点 ID")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	now := nowUnix()
	if _, err := s.db.Exec(`
		INSERT INTO pki_enrollment_tokens
			(token_hash, node_id, expires_at_unix, used_at_unix, created_at_unix)
		VALUES (?, ?, ?, 0, ?)`,
		hex.EncodeToString(sum[:]), nodeID, time.Now().Add(ttl).Unix(), now,
	); err != nil {
		return "", fmt.Errorf("创建注册令牌失败：%w", err)
	}
	return token, nil
}

// ConsumePKIEnrollmentToken atomically validates and burns a token.
func (s *Store) ConsumePKIEnrollmentToken(nodeID, token string) (bool, error) {
	sum := sha256.Sum256([]byte(token))
	now := nowUnix()
	res, err := s.db.Exec(`
		UPDATE pki_enrollment_tokens SET used_at_unix = ?
		WHERE token_hash = ? AND node_id = ? AND used_at_unix = 0
			AND expires_at_unix >= ?`,
		now, hex.EncodeToString(sum[:]), nodeID, now,
	)
	if err != nil {
		return false, fmt.Errorf("使用注册令牌失败：%w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkAgentEnrolled records that an uplink node finished token registration
// without a management certificate.
func (s *Store) MarkAgentEnrolled(nodeID string) error {
	if strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("必须提供节点 ID")
	}
	res, err := s.db.Exec(
		`UPDATE nodes SET agent_enrolled = 1, updated_at_unix = ? WHERE id = ?`,
		nowUnix(), nodeID,
	)
	if err != nil {
		return fmt.Errorf("记录节点注册状态失败：%w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("节点不存在：%s", nodeID)
	}
	return nil
}

// IsAgentEnrolled reports whether the node completed token registration.
// Push nodes stay false until they bind a management certificate; the install
// command uses the certificate serial for that case.
func (s *Store) IsAgentEnrolled(nodeID string) (bool, error) {
	var enrolled int
	err := s.db.QueryRow(`SELECT agent_enrolled FROM nodes WHERE id = ?`, nodeID).Scan(&enrolled)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("节点不存在：%s", nodeID)
	}
	if err != nil {
		return false, fmt.Errorf("读取节点注册状态失败：%w", err)
	}
	return enrolled != 0, nil
}

// ReplaceActivePKICertificate atomically records a newly issued certificate,
// retires the previous Agent certificate and binds the node to the new serial.
func (s *Store) ReplaceActivePKICertificate(cert *PKICertificate, caBundle string) error {
	if cert == nil || cert.Serial == "" || cert.NodeID == "" {
		return fmt.Errorf("必须提供证书序列号和节点 ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`UPDATE pki_certificates SET status = 'replaced'
		 WHERE node_id = ? AND profile = 'agent-server' AND status = 'active'`,
		cert.NodeID,
	); err != nil {
		return fmt.Errorf("停用当前证书失败：%w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO pki_certificates (
			serial, node_id, profile, subject, uri_san, dns_sans, ip_sans,
			not_before_unix, not_after_unix, status, revoked_at_unix,
			revoke_reason, cert_pem, created_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cert.Serial, cert.NodeID, cert.Profile, cert.Subject, cert.URISAN,
		cert.DNSSANs, cert.IPSANs, cert.NotBeforeUnix, cert.NotAfterUnix,
		cert.Status, cert.RevokedAtUnix, cert.RevokeReason, cert.CertPEM,
		cert.CreatedAtUnix,
	); err != nil {
		return fmt.Errorf("保存证书失败：%w", err)
	}
	if _, err := tx.Exec(`
		UPDATE nodes
		SET pki_ca_bundle_pem = ?, pki_cert_serial = ?,
			pki_not_after_unix = ?, updated_at_unix = ?
		WHERE id = ?`,
		caBundle, cert.Serial, cert.NotAfterUnix, nowUnix(), cert.NodeID,
	); err != nil {
		return fmt.Errorf("绑定节点证书失败：%w", err)
	}
	return tx.Commit()
}

func scanPKICertificate(row interface{ Scan(...any) error }) (*PKICertificate, error) {
	var c PKICertificate
	err := row.Scan(
		&c.Serial, &c.NodeID, &c.Profile, &c.Subject, &c.URISAN,
		&c.DNSSANs, &c.IPSANs, &c.NotBeforeUnix, &c.NotAfterUnix,
		&c.Status, &c.RevokedAtUnix, &c.RevokeReason, &c.CertPEM,
		&c.CreatedAtUnix,
	)
	return &c, err
}

const pkiCertificateCols = `serial, node_id, profile, subject, uri_san,
	dns_sans, ip_sans, not_before_unix, not_after_unix, status,
	revoked_at_unix, revoke_reason, cert_pem, created_at_unix`

func (s *Store) ListPKICertificates() ([]PKICertificate, error) {
	now := nowUnix()
	_, _ = s.db.Exec(`
		UPDATE pki_certificates SET status = 'expired'
		WHERE status = 'active' AND not_after_unix < ?`, now)
	_, _ = s.db.Exec(`
		UPDATE nodes SET pki_cert_serial = '', pki_not_after_unix = 0,
			status = 'unauthorized', last_error = '管理证书已过期',
			updated_at_unix = ?
		WHERE pki_cert_serial != '' AND pki_not_after_unix < ?`, now, now)
	rows, err := s.db.Query(`SELECT ` + pkiCertificateCols + `
		FROM pki_certificates ORDER BY created_at_unix DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询 PKI 证书列表失败：%w", err)
	}
	defer rows.Close()
	out := []PKICertificate{}
	for rows.Next() {
		c, err := scanPKICertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("读取 PKI 证书记录失败：%w", err)
		}
		c.CertPEM = ""
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) GetPKICertificate(serial string) (*PKICertificate, error) {
	c, err := scanPKICertificate(s.db.QueryRow(
		`SELECT `+pkiCertificateCols+` FROM pki_certificates WHERE serial = ?`,
		serial,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("PKI 证书不存在：%s", serial)
	}
	if err != nil {
		return nil, fmt.Errorf("读取 PKI 证书失败：%w", err)
	}
	return c, nil
}

func (s *Store) RevokePKICertificate(serial, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var nodeID string
	if err := tx.QueryRow(`SELECT node_id FROM pki_certificates WHERE serial = ?`, serial).Scan(&nodeID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("PKI 证书不存在：%s", serial)
		}
		return err
	}
	now := nowUnix()
	res, err := tx.Exec(`
		UPDATE pki_certificates
		SET status = 'revoked', revoked_at_unix = ?, revoke_reason = ?
		WHERE serial = ? AND status != 'revoked'`, now, reason, serial)
	if err != nil {
		return fmt.Errorf("吊销 PKI 证书失败：%w", err)
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return fmt.Errorf("证书已经吊销")
	}
	if nodeID != "" {
		if _, err := tx.Exec(`
			UPDATE nodes SET pki_cert_serial = '', pki_not_after_unix = 0,
				token = '',
				status = 'unauthorized', last_error = '管理证书已吊销',
				updated_at_unix = ?
			WHERE id = ? AND pki_cert_serial = ?`, now, nodeID, serial); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddPKIAudit(action, nodeID, serial, actor, detail string) error {
	_, err := s.db.Exec(`
		INSERT INTO pki_audit_logs
			(id, action, node_id, serial, actor, detail, created_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), action, nodeID, serial, actor, detail, nowUnix(),
	)
	if err != nil {
		return fmt.Errorf("写入 PKI 审计日志失败：%w", err)
	}
	return nil
}

func (s *Store) ListPKIAuditLogs(limit int) ([]PKIAuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT id, action, node_id, serial, actor, detail, created_at_unix
		FROM pki_audit_logs ORDER BY created_at_unix DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PKIAuditLog{}
	for rows.Next() {
		var a PKIAuditLog
		if err := rows.Scan(&a.ID, &a.Action, &a.NodeID, &a.Serial, &a.Actor, &a.Detail, &a.CreatedAtUnix); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
