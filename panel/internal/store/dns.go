package store

import (
	"database/sql"
	"fmt"
)

const dnsAccountCols = `id, name, provider, zone, credentials_ciphertext, settings_json,
	enabled, last_test_unix, last_test_error, created_at_unix, updated_at_unix`

func scanDNSAccount(row interface{ Scan(...any) error }) (*DNSAccount, error) {
	var account DNSAccount
	var settingsJSON string
	var enabled int
	if err := row.Scan(
		&account.ID, &account.Name, &account.Provider, &account.Zone,
		&account.CredentialsCiphertext, &settingsJSON, &enabled,
		&account.LastTestUnix, &account.LastTestError,
		&account.CreatedAtUnix, &account.UpdatedAtUnix,
	); err != nil {
		return nil, err
	}
	account.Enabled = enabled != 0
	account.HasCredentials = account.CredentialsCiphertext != ""
	account.Settings = map[string]any{}
	if err := unmarshalJSON(settingsJSON, &account.Settings); err != nil {
		return nil, fmt.Errorf("解析 DNS 账号设置失败：%w", err)
	}
	return &account, nil
}

func (s *Store) CreateDNSAccount(account *DNSAccount) error {
	if account == nil || account.Name == "" || account.Provider == "" || account.Zone == "" {
		return fmt.Errorf("必须提供 DNS 账号名称、供应商和区域")
	}
	settingsJSON, err := marshalJSON(account.Settings)
	if err != nil {
		return fmt.Errorf("编码 DNS 账号设置失败：%w", err)
	}
	if account.ID == "" {
		account.ID = newID()
	}
	now := nowUnix()
	account.CreatedAtUnix = now
	account.UpdatedAtUnix = now
	_, err = s.db.Exec(`
		INSERT INTO dns_accounts (
			id, name, provider, zone, credentials_ciphertext, settings_json, enabled,
			last_test_unix, last_test_error, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID, account.Name, account.Provider, account.Zone, account.CredentialsCiphertext,
		settingsJSON, boolToInt(account.Enabled), account.LastTestUnix,
		account.LastTestError, now, now,
	)
	if err != nil {
		return fmt.Errorf("创建 DNS 账号失败：%w", err)
	}
	account.HasCredentials = account.CredentialsCiphertext != ""
	return nil
}

func (s *Store) UpdateDNSAccount(account *DNSAccount) error {
	if account == nil || account.ID == "" || account.Name == "" ||
		account.Provider == "" || account.Zone == "" {
		return fmt.Errorf("必须提供完整的 DNS 账号")
	}
	var incompatibleDomains int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM managed_domains
		WHERE dns_account_id = ? AND zone <> ?`,
		account.ID, account.Zone,
	).Scan(&incompatibleDomains); err != nil {
		return fmt.Errorf("检查 DNS 账号区域引用失败：%w", err)
	}
	if incompatibleDomains > 0 {
		return fmt.Errorf("DNS 账号已关联其他区域的托管域名；请为新区域创建独立账号")
	}
	settingsJSON, err := marshalJSON(account.Settings)
	if err != nil {
		return fmt.Errorf("编码 DNS 账号设置失败：%w", err)
	}
	account.UpdatedAtUnix = nowUnix()
	res, err := s.db.Exec(`
		UPDATE dns_accounts SET name = ?, provider = ?, zone = ?, credentials_ciphertext = ?,
			settings_json = ?, enabled = ?, last_test_unix = ?,
			last_test_error = ?, updated_at_unix = ?
		WHERE id = ?`,
		account.Name, account.Provider, account.Zone, account.CredentialsCiphertext, settingsJSON,
		boolToInt(account.Enabled), account.LastTestUnix, account.LastTestError,
		account.UpdatedAtUnix, account.ID,
	)
	if err != nil {
		return fmt.Errorf("更新 DNS 账号失败：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("DNS 账号不存在：%s", account.ID)
	}
	account.HasCredentials = account.CredentialsCiphertext != ""
	return nil
}

func (s *Store) GetDNSAccount(id string) (*DNSAccount, error) {
	account, err := scanDNSAccount(s.db.QueryRow(
		`SELECT `+dnsAccountCols+` FROM dns_accounts WHERE id = ?`, id,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("DNS 账号不存在：%s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("读取 DNS 账号失败：%w", err)
	}
	return account, nil
}

func (s *Store) ListDNSAccounts() ([]DNSAccount, error) {
	rows, err := s.db.Query(`SELECT ` + dnsAccountCols + ` FROM dns_accounts ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("查询 DNS 账号失败：%w", err)
	}
	defer rows.Close()
	out := []DNSAccount{}
	for rows.Next() {
		account, err := scanDNSAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("读取 DNS 账号失败：%w", err)
		}
		out = append(out, *account)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDNSAccount(id string) error {
	res, err := s.db.Exec(`DELETE FROM dns_accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 DNS 账号失败；请先解除关联域名：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("DNS 账号不存在：%s", id)
	}
	return nil
}

const managedDomainCols = `id, node_id, dns_account_id, zone, fqdn, record_mode,
	address_source, manual_ipv4, manual_ipv6, ttl, enabled, state,
	desired_ipv4, desired_ipv6, observed_ipv4_json, observed_ipv6_json,
	provider_record_a_id, provider_record_aaaa_id, created_a_by_panel,
	created_aaaa_by_panel, last_reconcile_unix, next_reconcile_unix,
	retry_count, last_error, created_at_unix, updated_at_unix`

func scanManagedDomain(row interface{ Scan(...any) error }) (*ManagedDomain, error) {
	var domain ManagedDomain
	var enabled, createdA, createdAAAA int
	var observedIPv4JSON, observedIPv6JSON string
	if err := row.Scan(
		&domain.ID, &domain.NodeID, &domain.DNSAccountID, &domain.Zone, &domain.FQDN,
		&domain.RecordMode, &domain.AddressSource, &domain.ManualIPv4,
		&domain.ManualIPv6, &domain.TTL, &enabled, &domain.State,
		&domain.DesiredIPv4, &domain.DesiredIPv6, &observedIPv4JSON,
		&observedIPv6JSON, &domain.ProviderRecordAID, &domain.ProviderRecordAAAAID,
		&createdA, &createdAAAA, &domain.LastReconcileUnix,
		&domain.NextReconcileUnix, &domain.RetryCount, &domain.LastError,
		&domain.CreatedAtUnix, &domain.UpdatedAtUnix,
	); err != nil {
		return nil, err
	}
	domain.Enabled = enabled != 0
	domain.CreatedAByPanel = createdA != 0
	domain.CreatedAAAAByPanel = createdAAAA != 0
	domain.ObservedIPv4 = []string{}
	domain.ObservedIPv6 = []string{}
	if err := unmarshalJSON(observedIPv4JSON, &domain.ObservedIPv4); err != nil {
		return nil, fmt.Errorf("解析域名 IPv4 观测值失败：%w", err)
	}
	if err := unmarshalJSON(observedIPv6JSON, &domain.ObservedIPv6); err != nil {
		return nil, fmt.Errorf("解析域名 IPv6 观测值失败：%w", err)
	}
	return &domain, nil
}

func (s *Store) CreateManagedDomain(domain *ManagedDomain) error {
	if domain == nil || domain.NodeID == "" || domain.DNSAccountID == "" ||
		domain.Zone == "" || domain.FQDN == "" {
		return fmt.Errorf("必须提供节点、DNS 账号、区域和完整域名")
	}
	if err := s.validateManagedDomainZone(domain.DNSAccountID, domain.Zone); err != nil {
		return err
	}
	if domain.ID == "" {
		domain.ID = newID()
	}
	if domain.RecordMode == "" {
		domain.RecordMode = "a"
	}
	if domain.AddressSource == "" {
		domain.AddressSource = "manual"
	}
	if domain.TTL == 0 {
		domain.TTL = 300
	}
	if domain.State == "" {
		domain.State = "pending"
	}
	observedIPv4JSON, err := marshalJSON(domain.ObservedIPv4)
	if err != nil {
		return err
	}
	observedIPv6JSON, err := marshalJSON(domain.ObservedIPv6)
	if err != nil {
		return err
	}
	now := nowUnix()
	domain.CreatedAtUnix = now
	domain.UpdatedAtUnix = now
	_, err = s.db.Exec(`
		INSERT INTO managed_domains (
			id, node_id, dns_account_id, zone, fqdn, record_mode, address_source,
			manual_ipv4, manual_ipv6, ttl, enabled, state, desired_ipv4,
			desired_ipv6, observed_ipv4_json, observed_ipv6_json,
			provider_record_a_id, provider_record_aaaa_id, created_a_by_panel,
			created_aaaa_by_panel, last_reconcile_unix, next_reconcile_unix,
			retry_count, last_error, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		domain.ID, domain.NodeID, domain.DNSAccountID, domain.Zone, domain.FQDN,
		domain.RecordMode, domain.AddressSource, domain.ManualIPv4, domain.ManualIPv6,
		domain.TTL, boolToInt(domain.Enabled), domain.State, domain.DesiredIPv4,
		domain.DesiredIPv6, observedIPv4JSON, observedIPv6JSON,
		domain.ProviderRecordAID, domain.ProviderRecordAAAAID,
		boolToInt(domain.CreatedAByPanel), boolToInt(domain.CreatedAAAAByPanel),
		domain.LastReconcileUnix, domain.NextReconcileUnix, domain.RetryCount,
		domain.LastError, now, now,
	)
	if err != nil {
		return fmt.Errorf("创建托管域名失败：%w", err)
	}
	return nil
}

func (s *Store) UpdateManagedDomain(domain *ManagedDomain) error {
	if domain == nil || domain.ID == "" {
		return fmt.Errorf("必须提供托管域名 ID")
	}
	if err := s.validateManagedDomainZone(domain.DNSAccountID, domain.Zone); err != nil {
		return err
	}
	observedIPv4JSON, err := marshalJSON(domain.ObservedIPv4)
	if err != nil {
		return err
	}
	observedIPv6JSON, err := marshalJSON(domain.ObservedIPv6)
	if err != nil {
		return err
	}
	domain.UpdatedAtUnix = nowUnix()
	res, err := s.db.Exec(`
		UPDATE managed_domains SET node_id = ?, dns_account_id = ?, zone = ?,
			fqdn = ?, record_mode = ?, address_source = ?, manual_ipv4 = ?,
			manual_ipv6 = ?, ttl = ?, enabled = ?, state = ?, desired_ipv4 = ?,
			desired_ipv6 = ?, observed_ipv4_json = ?, observed_ipv6_json = ?,
			provider_record_a_id = ?, provider_record_aaaa_id = ?,
			created_a_by_panel = ?, created_aaaa_by_panel = ?,
			last_reconcile_unix = ?, next_reconcile_unix = ?, retry_count = ?,
			last_error = ?, updated_at_unix = ?
		WHERE id = ?`,
		domain.NodeID, domain.DNSAccountID, domain.Zone, domain.FQDN,
		domain.RecordMode, domain.AddressSource, domain.ManualIPv4,
		domain.ManualIPv6, domain.TTL, boolToInt(domain.Enabled), domain.State,
		domain.DesiredIPv4, domain.DesiredIPv6, observedIPv4JSON,
		observedIPv6JSON, domain.ProviderRecordAID, domain.ProviderRecordAAAAID,
		boolToInt(domain.CreatedAByPanel), boolToInt(domain.CreatedAAAAByPanel),
		domain.LastReconcileUnix, domain.NextReconcileUnix, domain.RetryCount,
		domain.LastError, domain.UpdatedAtUnix, domain.ID,
	)
	if err != nil {
		return fmt.Errorf("更新托管域名失败：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("托管域名不存在：%s", domain.ID)
	}
	return nil
}

func (s *Store) validateManagedDomainZone(accountID, zone string) error {
	var accountZone string
	if err := s.db.QueryRow(
		`SELECT zone FROM dns_accounts WHERE id = ?`, accountID,
	).Scan(&accountZone); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("DNS 账号不存在：%s", accountID)
		}
		return fmt.Errorf("读取 DNS 账号区域失败：%w", err)
	}
	if accountZone == "" {
		return fmt.Errorf("DNS 账号尚未配置区域")
	}
	if accountZone != zone {
		return fmt.Errorf("托管域名区域必须与 DNS 账号区域一致：%s", accountZone)
	}
	return nil
}

func (s *Store) GetManagedDomain(id string) (*ManagedDomain, error) {
	domain, err := scanManagedDomain(s.db.QueryRow(
		`SELECT `+managedDomainCols+` FROM managed_domains WHERE id = ?`, id,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("托管域名不存在：%s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("读取托管域名失败：%w", err)
	}
	return domain, nil
}

func (s *Store) ListManagedDomains() ([]ManagedDomain, error) {
	rows, err := s.db.Query(`SELECT ` + managedDomainCols + `
		FROM managed_domains ORDER BY fqdn`)
	if err != nil {
		return nil, fmt.Errorf("查询托管域名失败：%w", err)
	}
	defer rows.Close()
	out := []ManagedDomain{}
	for rows.Next() {
		domain, err := scanManagedDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("读取托管域名失败：%w", err)
		}
		out = append(out, *domain)
	}
	return out, rows.Err()
}

// ListManagedDomainsByNode returns managed domains bound to one node.
func (s *Store) ListManagedDomainsByNode(nodeID string) ([]ManagedDomain, error) {
	rows, err := s.db.Query(`SELECT `+managedDomainCols+`
		FROM managed_domains WHERE node_id = ? ORDER BY fqdn`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("查询节点托管域名失败：%w", err)
	}
	defer rows.Close()
	out := []ManagedDomain{}
	for rows.Next() {
		domain, err := scanManagedDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("读取托管域名失败：%w", err)
		}
		out = append(out, *domain)
	}
	return out, rows.Err()
}

func (s *Store) ListManagedDomainsDue(now int64, limit int) ([]ManagedDomain, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+managedDomainCols+`
		FROM managed_domains
		WHERE enabled = 1 AND next_reconcile_unix <= ?
		ORDER BY next_reconcile_unix, created_at_unix LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待同步域名失败：%w", err)
	}
	defer rows.Close()
	out := []ManagedDomain{}
	for rows.Next() {
		domain, err := scanManagedDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *domain)
	}
	return out, rows.Err()
}

func (s *Store) DeleteManagedDomain(id string) error {
	certificates, bindings, err := s.ManagedDomainTLSUsage(id)
	if err != nil {
		return err
	}
	if certificates > 0 || bindings > 0 {
		return fmt.Errorf(
			"删除托管域名失败；请先解除 %d 个协议证书和 %d 个 TLS 绑定",
			certificates, bindings,
		)
	}
	res, err := s.db.Exec(`DELETE FROM managed_domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除托管域名失败：%w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("托管域名不存在：%s", id)
	}
	return nil
}

func (s *Store) ManagedDomainTLSUsage(id string) (certificates, bindings int, err error) {
	if err = s.db.QueryRow(
		`SELECT COUNT(*) FROM protocol_certificates WHERE managed_domain_id = ?`, id,
	).Scan(&certificates); err != nil {
		return 0, 0, fmt.Errorf("检查托管域名证书引用失败：%w", err)
	}
	if err = s.db.QueryRow(
		`SELECT COUNT(*) FROM node_inbound_tls_bindings WHERE managed_domain_id = ?`, id,
	).Scan(&bindings); err != nil {
		return 0, 0, fmt.Errorf("检查托管域名 TLS 引用失败：%w", err)
	}
	return certificates, bindings, nil
}
