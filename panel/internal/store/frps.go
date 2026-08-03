package store

import (
	"database/sql"
	"fmt"
)

const frpsConfigCols = `node_id, enabled, bind_addr, bind_port, proxy_bind_addr,
	allow_ports_json, auth_token_ciphertext, tls_force, max_ports_per_client,
	desired_hash, applied_hash, runtime_state, frps_version, last_error,
	started_at_unix, created_at_unix, updated_at_unix, managed_domain_id`

func scanFRPServerConfig(row interface{ Scan(...any) error }) (*FRPServerConfig, error) {
	var config FRPServerConfig
	var enabled, tlsForce int
	var allowPortsJSON string
	var managedDomainID sql.NullString
	if err := row.Scan(
		&config.NodeID, &enabled, &config.BindAddr, &config.BindPort,
		&config.ProxyBindAddr, &allowPortsJSON, &config.AuthTokenCiphertext,
		&tlsForce, &config.MaxPortsPerClient, &config.DesiredHash,
		&config.AppliedHash, &config.RuntimeState, &config.FRPSVersion,
		&config.LastError, &config.StartedAtUnix, &config.CreatedAtUnix,
		&config.UpdatedAtUnix, &managedDomainID,
	); err != nil {
		return nil, err
	}
	config.Enabled = enabled != 0
	config.TLSForce = tlsForce != 0
	config.ManagedDomainID = managedDomainID.String
	config.HasAuthToken = config.AuthTokenCiphertext != ""
	config.AllowPorts = []FRPServerPortRange{}
	if err := unmarshalJSON(allowPortsJSON, &config.AllowPorts); err != nil {
		return nil, fmt.Errorf("解析节点 FRPS 允许端口失败：%w", err)
	}
	return &config, nil
}

func (s *Store) GetFRPServerConfig(nodeID string) (*FRPServerConfig, error) {
	config, err := scanFRPServerConfig(s.db.QueryRow(
		`SELECT `+frpsConfigCols+` FROM node_frps_configs WHERE node_id = ?`, nodeID,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("节点 FRPS 配置不存在：%s", nodeID)
	}
	if err != nil {
		return nil, fmt.Errorf("读取节点 FRPS 配置失败：%w", err)
	}
	return config, nil
}

func (s *Store) UpsertFRPServerConfig(config *FRPServerConfig) error {
	if config == nil || config.NodeID == "" {
		return fmt.Errorf("必须提供节点 FRPS 配置")
	}
	allowPortsJSON, err := marshalJSON(config.AllowPorts)
	if err != nil {
		return fmt.Errorf("编码节点 FRPS 允许端口失败：%w", err)
	}
	now := nowUnix()
	if config.CreatedAtUnix == 0 {
		config.CreatedAtUnix = now
	}
	config.UpdatedAtUnix = now
	managedDomainID := sql.NullString{String: config.ManagedDomainID, Valid: config.ManagedDomainID != ""}
	_, err = s.db.Exec(`
		INSERT INTO node_frps_configs (
			node_id, enabled, bind_addr, bind_port, proxy_bind_addr,
			allow_ports_json, auth_token_ciphertext, tls_force,
			max_ports_per_client, desired_hash, applied_hash, runtime_state,
			frps_version, last_error, started_at_unix, created_at_unix,
			updated_at_unix, managed_domain_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			enabled = excluded.enabled,
			bind_addr = excluded.bind_addr,
			bind_port = excluded.bind_port,
			proxy_bind_addr = excluded.proxy_bind_addr,
			allow_ports_json = excluded.allow_ports_json,
			auth_token_ciphertext = excluded.auth_token_ciphertext,
			tls_force = excluded.tls_force,
			max_ports_per_client = excluded.max_ports_per_client,
			desired_hash = excluded.desired_hash,
			applied_hash = excluded.applied_hash,
			runtime_state = excluded.runtime_state,
			frps_version = excluded.frps_version,
			last_error = excluded.last_error,
			started_at_unix = excluded.started_at_unix,
			updated_at_unix = excluded.updated_at_unix,
			managed_domain_id = excluded.managed_domain_id`,
		config.NodeID, boolToInt(config.Enabled), config.BindAddr,
		config.BindPort, config.ProxyBindAddr, allowPortsJSON,
		config.AuthTokenCiphertext, boolToInt(config.TLSForce),
		config.MaxPortsPerClient, config.DesiredHash, config.AppliedHash,
		config.RuntimeState, config.FRPSVersion, config.LastError,
		config.StartedAtUnix, config.CreatedAtUnix, config.UpdatedAtUnix,
		managedDomainID,
	)
	if err != nil {
		return fmt.Errorf("保存节点 FRPS 配置失败：%w", err)
	}
	config.HasAuthToken = config.AuthTokenCiphertext != ""
	return nil
}

func (s *Store) UpdateFRPServerRuntime(
	nodeID, appliedHash, state, version, lastError string,
	startedAtUnix int64,
) error {
	res, err := s.db.Exec(`
		UPDATE node_frps_configs
		SET applied_hash = ?, runtime_state = ?, frps_version = ?,
			last_error = ?, started_at_unix = ?, updated_at_unix = ?
		WHERE node_id = ?`,
		appliedHash, state, version, lastError, startedAtUnix, nowUnix(), nodeID,
	)
	if err != nil {
		return fmt.Errorf("更新节点 FRPS 运行状态失败：%w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("节点 FRPS 配置不存在：%s", nodeID)
	}
	return nil
}
