package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed persistence layer for the panel.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and ensures schema + defaults.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-writer is typical for panel; keep it simple and reliable.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureDefaultSettings(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			grpc_port INTEGER NOT NULL,
			token TEXT NOT NULL DEFAULT '',
			labels_json TEXT NOT NULL DEFAULT '[]',
			tls_skip_verify INTEGER NOT NULL DEFAULT 0,
			ca_cert_pem TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			last_seen_unix INTEGER NOT NULL DEFAULT 0,
			config_hash TEXT NOT NULL DEFAULT '',
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS inbounds (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			params_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_inbounds (
			node_id TEXT NOT NULL,
			inbound_id TEXT NOT NULL,
			PRIMARY KEY (node_id, inbound_id),
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (inbound_id) REFERENCES inbounds(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			node_ids_json TEXT NOT NULL DEFAULT '[]',
			results_json TEXT NOT NULL DEFAULT '[]',
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			admin_password_hash TEXT NOT NULL DEFAULT '',
			default_agent_token TEXT NOT NULL DEFAULT '',
			grpc_timeout_sec INTEGER NOT NULL DEFAULT 10,
			max_concurrency INTEGER NOT NULL DEFAULT 10,
			listen_addr TEXT NOT NULL DEFAULT ':8080',
			public_base_url TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			format TEXT NOT NULL,
			token TEXT NOT NULL UNIQUE,
			inbound_ids_json TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at_unix INTEGER NOT NULL,
			updated_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS config_snapshots (
			id TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			config_json TEXT NOT NULL,
			config_hash TEXT NOT NULL,
			task_id TEXT NOT NULL DEFAULT '',
			created_at_unix INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_config_snapshots_node_created
			ON config_snapshots(node_id, created_at_unix DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Additive columns for fleet monitoring (safe to re-run).
	alters := []string{
		`ALTER TABLE nodes ADD COLUMN runtime_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN agent_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN singbox_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN connections INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN uplink_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN downlink_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN cpu_percent REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN memory_rss_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN metrics_at_unix INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN egress_interface TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN public_address TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN port_mappings_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE node_inbounds ADD COLUMN public_address TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE node_inbounds ADD COLUMN public_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN public_base_url TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		_, _ = s.db.Exec(stmt) // ignore "duplicate column" on existing DBs
	}
	return nil
}

func (s *Store) ensureDefaultSettings() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE id = 1`).Scan(&n); err != nil {
		return fmt.Errorf("check settings: %w", err)
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO settings (id, admin_password_hash, default_agent_token, grpc_timeout_sec, max_concurrency, listen_addr)
		VALUES (1, '', '', 10, 10, ':8080')`)
	if err != nil {
		return fmt.Errorf("insert default settings: %w", err)
	}
	return nil
}

func nowUnix() int64 {
	return time.Now().Unix()
}

func newID() string {
	return uuid.NewString()
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalJSON[T any](s string, dest *T) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dest)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Nodes ---

func (s *Store) CreateNode(n *Node) error {
	if n == nil {
		return fmt.Errorf("node is nil")
	}
	if n.ID == "" {
		n.ID = newID()
	}
	now := nowUnix()
	if n.CreatedAtUnix == 0 {
		n.CreatedAtUnix = now
	}
	if n.UpdatedAtUnix == 0 {
		n.UpdatedAtUnix = now
	}
	if n.Labels == nil {
		n.Labels = []string{}
	}
	n.PortMappings = NormalizePortMappings(n.PortMappings)
	labelsJSON, err := marshalJSON(n.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	mappingsJSON, err := marshalJSON(n.PortMappings)
	if err != nil {
		return fmt.Errorf("marshal port_mappings: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO nodes (
			id, name, address, grpc_port, token, labels_json, tls_skip_verify, ca_cert_pem,
			status, last_seen_unix, config_hash,
			runtime_state, agent_version, singbox_version,
			connections, uplink_bytes, downlink_bytes, cpu_percent, memory_rss_bytes,
			metrics_at_unix, last_error, egress_interface, public_address, port_mappings_json,
			created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Name, n.Address, n.GRPCPort, n.Token, labelsJSON, boolToInt(n.TLSSkipVerify), n.CACertPEM,
		n.Status, n.LastSeenUnix, n.ConfigHash,
		n.RuntimeState, n.AgentVersion, n.SingboxVersion,
		n.Connections, n.UplinkBytes, n.DownlinkBytes, n.CPUPercent, n.MemoryRSSBytes,
		n.MetricsAtUnix, n.LastError, n.EgressInterface, n.PublicAddress, mappingsJSON,
		n.CreatedAtUnix, n.UpdatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}
	return nil
}

func (s *Store) UpdateNode(n *Node) error {
	if n == nil || n.ID == "" {
		return fmt.Errorf("node id required")
	}
	n.UpdatedAtUnix = nowUnix()
	if n.Labels == nil {
		n.Labels = []string{}
	}
	n.PortMappings = NormalizePortMappings(n.PortMappings)
	labelsJSON, err := marshalJSON(n.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	mappingsJSON, err := marshalJSON(n.PortMappings)
	if err != nil {
		return fmt.Errorf("marshal port_mappings: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE nodes SET
			name = ?, address = ?, grpc_port = ?, token = ?, labels_json = ?,
			tls_skip_verify = ?, ca_cert_pem = ?, status = ?, last_seen_unix = ?,
			config_hash = ?,
			runtime_state = ?, agent_version = ?, singbox_version = ?,
			connections = ?, uplink_bytes = ?, downlink_bytes = ?, cpu_percent = ?, memory_rss_bytes = ?,
			metrics_at_unix = ?, last_error = ?, egress_interface = ?, public_address = ?,
			port_mappings_json = ?,
			updated_at_unix = ?
		WHERE id = ?`,
		n.Name, n.Address, n.GRPCPort, n.Token, labelsJSON,
		boolToInt(n.TLSSkipVerify), n.CACertPEM, n.Status, n.LastSeenUnix,
		n.ConfigHash,
		n.RuntimeState, n.AgentVersion, n.SingboxVersion,
		n.Connections, n.UplinkBytes, n.DownlinkBytes, n.CPUPercent, n.MemoryRSSBytes,
		n.MetricsAtUnix, n.LastError, n.EgressInterface, n.PublicAddress,
		mappingsJSON,
		n.UpdatedAtUnix, n.ID,
	)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("node not found: %s", n.ID)
	}
	return nil
}

func (s *Store) DeleteNode(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM node_inbounds WHERE node_id = ?`, id); err != nil {
		return fmt.Errorf("delete node_inbounds: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("node not found: %s", id)
	}
	return tx.Commit()
}

func scanNode(row interface {
	Scan(dest ...any) error
}) (*Node, error) {
	var n Node
	var labelsJSON string
	var mappingsJSON string
	var tlsSkip int
	err := row.Scan(
		&n.ID, &n.Name, &n.Address, &n.GRPCPort, &n.Token, &labelsJSON, &tlsSkip, &n.CACertPEM,
		&n.Status, &n.LastSeenUnix, &n.ConfigHash,
		&n.RuntimeState, &n.AgentVersion, &n.SingboxVersion,
		&n.Connections, &n.UplinkBytes, &n.DownlinkBytes, &n.CPUPercent, &n.MemoryRSSBytes,
		&n.MetricsAtUnix, &n.LastError, &n.EgressInterface, &n.PublicAddress, &mappingsJSON,
		&n.CreatedAtUnix, &n.UpdatedAtUnix,
	)
	if err != nil {
		return nil, err
	}
	n.TLSSkipVerify = tlsSkip != 0
	n.Labels = []string{}
	if err := unmarshalJSON(labelsJSON, &n.Labels); err != nil {
		return nil, fmt.Errorf("unmarshal labels: %w", err)
	}
	n.PortMappings = []PortMapping{}
	if err := unmarshalJSON(mappingsJSON, &n.PortMappings); err != nil {
		return nil, fmt.Errorf("unmarshal port_mappings: %w", err)
	}
	n.PortMappings = NormalizePortMappings(n.PortMappings)
	return &n, nil
}

const nodeSelectCols = `id, name, address, grpc_port, token, labels_json, tls_skip_verify, ca_cert_pem,
	status, last_seen_unix, config_hash,
	runtime_state, agent_version, singbox_version,
	connections, uplink_bytes, downlink_bytes, cpu_percent, memory_rss_bytes,
	metrics_at_unix, last_error, egress_interface, public_address, port_mappings_json,
	created_at_unix, updated_at_unix`

func (s *Store) GetNode(id string) (*Node, error) {
	row := s.db.QueryRow(`SELECT `+nodeSelectCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("node not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return n, nil
}

// GetNodeByToken returns the unique node with this non-empty control token.
// Empty token is rejected. Multiple matches return an error (ambiguous).
func (s *Store) GetNodeByToken(token string) (*Node, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token required")
	}
	rows, err := s.db.Query(`SELECT `+nodeSelectCols+` FROM nodes WHERE token = ?`, token)
	if err != nil {
		return nil, fmt.Errorf("get node by token: %w", err)
	}
	defer rows.Close()
	var found *Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("get node by token scan: %w", err)
		}
		if found != nil {
			return nil, fmt.Errorf("multiple nodes share this token")
		}
		found = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("node not found for token")
	}
	return found, nil
}

func (s *Store) ListNodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeSelectCols + ` FROM nodes ORDER BY created_at_unix ASC`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("list nodes scan: %w", err)
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Node{}
	}
	return out, nil
}

// ListNodesByLabels returns nodes that have any of the given labels.
func (s *Store) ListNodesByLabels(labels []string) ([]Node, error) {
	if len(labels) == 0 {
		return []Node{}, nil
	}
	nodes, err := s.ListNodes()
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		want[l] = struct{}{}
	}
	var out []Node
	for _, n := range nodes {
		for _, l := range n.Labels {
			if _, ok := want[l]; ok {
				out = append(out, n)
				break
			}
		}
	}
	if out == nil {
		out = []Node{}
	}
	return out, nil
}

// --- Inbounds ---

func (s *Store) CreateInbound(in *InboundConfig) error {
	if in == nil {
		return fmt.Errorf("inbound is nil")
	}
	if in.ID == "" {
		in.ID = newID()
	}
	now := nowUnix()
	if in.CreatedAtUnix == 0 {
		in.CreatedAtUnix = now
	}
	if in.UpdatedAtUnix == 0 {
		in.UpdatedAtUnix = now
	}
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	paramsJSON, err := marshalJSON(in.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO inbounds (id, name, protocol, params_json, enabled, created_at_unix, updated_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Name, in.Protocol, paramsJSON, boolToInt(in.Enabled), in.CreatedAtUnix, in.UpdatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("create inbound: %w", err)
	}
	return nil
}

func (s *Store) UpdateInbound(in *InboundConfig) error {
	if in == nil || in.ID == "" {
		return fmt.Errorf("inbound id required")
	}
	in.UpdatedAtUnix = nowUnix()
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	paramsJSON, err := marshalJSON(in.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE inbounds SET name = ?, protocol = ?, params_json = ?, enabled = ?, updated_at_unix = ?
		WHERE id = ?`,
		in.Name, in.Protocol, paramsJSON, boolToInt(in.Enabled), in.UpdatedAtUnix, in.ID,
	)
	if err != nil {
		return fmt.Errorf("update inbound: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("inbound not found: %s", in.ID)
	}
	return nil
}

func (s *Store) DeleteInbound(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM node_inbounds WHERE inbound_id = ?`, id); err != nil {
		return fmt.Errorf("delete node_inbounds: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM inbounds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete inbound: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("inbound not found: %s", id)
	}
	return tx.Commit()
}

func scanInbound(row interface {
	Scan(dest ...any) error
}) (*InboundConfig, error) {
	var in InboundConfig
	var paramsJSON string
	var enabled int
	err := row.Scan(&in.ID, &in.Name, &in.Protocol, &paramsJSON, &enabled, &in.CreatedAtUnix, &in.UpdatedAtUnix)
	if err != nil {
		return nil, err
	}
	in.Enabled = enabled != 0
	in.Params = map[string]any{}
	if err := unmarshalJSON(paramsJSON, &in.Params); err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}
	return &in, nil
}

const inboundSelectCols = `id, name, protocol, params_json, enabled, created_at_unix, updated_at_unix`

func (s *Store) GetInbound(id string) (*InboundConfig, error) {
	row := s.db.QueryRow(`SELECT `+inboundSelectCols+` FROM inbounds WHERE id = ?`, id)
	in, err := scanInbound(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("inbound not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get inbound: %w", err)
	}
	return in, nil
}

func (s *Store) ListInbounds() ([]InboundConfig, error) {
	rows, err := s.db.Query(`SELECT ` + inboundSelectCols + ` FROM inbounds ORDER BY created_at_unix ASC`)
	if err != nil {
		return nil, fmt.Errorf("list inbounds: %w", err)
	}
	defer rows.Close()

	var out []InboundConfig
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, fmt.Errorf("list inbounds scan: %w", err)
		}
		out = append(out, *in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []InboundConfig{}
	}
	return out, nil
}

// --- Attachments ---

// SetNodeInbounds attaches inbound IDs without NAT overrides (compat helper).
func (s *Store) SetNodeInbounds(nodeID string, inboundIDs []string) error {
	bindings := make([]NodeInboundBinding, 0, len(inboundIDs))
	for _, id := range inboundIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		bindings = append(bindings, NodeInboundBinding{InboundID: id})
	}
	return s.SetNodeInboundBindings(nodeID, bindings)
}

// SetNodeInboundBindings replaces attachments for a node, including optional per-inbound NAT fields.
func (s *Store) SetNodeInboundBindings(nodeID string, bindings []NodeInboundBinding) error {
	// Ensure node exists.
	if _, err := s.GetNode(nodeID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM node_inbounds WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("clear node_inbounds: %w", err)
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		iid := strings.TrimSpace(b.InboundID)
		if iid == "" || seen[iid] {
			continue
		}
		seen[iid] = true
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM inbounds WHERE id = ?`, iid).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("inbound not found: %s", iid)
		}
		pubAddr := strings.TrimSpace(b.PublicAddress)
		pubPort := b.PublicPort
		if pubPort < 0 || pubPort > 65535 {
			return fmt.Errorf("public_port out of range for inbound %s: %d", iid, pubPort)
		}
		if _, err := tx.Exec(
			`INSERT INTO node_inbounds (node_id, inbound_id, public_address, public_port) VALUES (?, ?, ?, ?)`,
			nodeID, iid, pubAddr, pubPort,
		); err != nil {
			return fmt.Errorf("attach inbound: %w", err)
		}
	}
	return tx.Commit()
}

// ListInboundsForNode returns attached inbound configs (without NAT overrides).
// Prefer ListNodeInboundAttachments when subscription NAT fields are needed.
func (s *Store) ListInboundsForNode(nodeID string) ([]InboundConfig, error) {
	atts, err := s.ListNodeInboundAttachments(nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]InboundConfig, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.InboundConfig)
	}
	return out, nil
}

// ListNodeInboundAttachments returns attached inbounds with per-inbound public host/port overrides.
func (s *Store) ListNodeInboundAttachments(nodeID string) ([]NodeInboundAttachment, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.name, i.protocol, i.params_json, i.enabled, i.created_at_unix, i.updated_at_unix,
			COALESCE(ni.public_address, ''), COALESCE(ni.public_port, 0)
		FROM inbounds i
		INNER JOIN node_inbounds ni ON ni.inbound_id = i.id
		WHERE ni.node_id = ?
		ORDER BY i.created_at_unix ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node inbound attachments: %w", err)
	}
	defer rows.Close()

	var out []NodeInboundAttachment
	for rows.Next() {
		var a NodeInboundAttachment
		var paramsJSON string
		var enabled int
		err := rows.Scan(
			&a.ID, &a.Name, &a.Protocol, &paramsJSON, &enabled, &a.CreatedAtUnix, &a.UpdatedAtUnix,
			&a.PublicAddress, &a.PublicPort,
		)
		if err != nil {
			return nil, fmt.Errorf("list node inbound attachments scan: %w", err)
		}
		a.Enabled = enabled != 0
		a.Params = map[string]any{}
		if err := unmarshalJSON(paramsJSON, &a.Params); err != nil {
			return nil, fmt.Errorf("unmarshal inbound params: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []NodeInboundAttachment{}
	}
	return out, nil
}

// CountInboundsByNode returns node_id -> attached inbound count.
func (s *Store) CountInboundsByNode() (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT node_id, COUNT(*) FROM node_inbounds GROUP BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("count inbounds by node: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// --- Tasks ---

func (s *Store) CreateTask(t *Task) error {
	if t == nil {
		return fmt.Errorf("task is nil")
	}
	if t.ID == "" {
		t.ID = newID()
	}
	now := nowUnix()
	if t.CreatedAtUnix == 0 {
		t.CreatedAtUnix = now
	}
	if t.UpdatedAtUnix == 0 {
		t.UpdatedAtUnix = now
	}
	if t.NodeIDs == nil {
		t.NodeIDs = []string{}
	}
	if t.Results == nil {
		t.Results = []TaskNodeResult{}
	}
	nodeIDsJSON, err := marshalJSON(t.NodeIDs)
	if err != nil {
		return fmt.Errorf("marshal node_ids: %w", err)
	}
	resultsJSON, err := marshalJSON(t.Results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO tasks (id, type, status, node_ids_json, results_json, created_at_unix, updated_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Type, t.Status, nodeIDsJSON, resultsJSON, t.CreatedAtUnix, t.UpdatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Store) UpdateTask(t *Task) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("task id required")
	}
	t.UpdatedAtUnix = nowUnix()
	if t.NodeIDs == nil {
		t.NodeIDs = []string{}
	}
	if t.Results == nil {
		t.Results = []TaskNodeResult{}
	}
	nodeIDsJSON, err := marshalJSON(t.NodeIDs)
	if err != nil {
		return fmt.Errorf("marshal node_ids: %w", err)
	}
	resultsJSON, err := marshalJSON(t.Results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE tasks SET type = ?, status = ?, node_ids_json = ?, results_json = ?, updated_at_unix = ?
		WHERE id = ?`,
		t.Type, t.Status, nodeIDsJSON, resultsJSON, t.UpdatedAtUnix, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task not found: %s", t.ID)
	}
	return nil
}

func scanTask(row interface {
	Scan(dest ...any) error
}) (*Task, error) {
	var t Task
	var nodeIDsJSON, resultsJSON string
	err := row.Scan(&t.ID, &t.Type, &t.Status, &nodeIDsJSON, &resultsJSON, &t.CreatedAtUnix, &t.UpdatedAtUnix)
	if err != nil {
		return nil, err
	}
	t.NodeIDs = []string{}
	t.Results = []TaskNodeResult{}
	if err := unmarshalJSON(nodeIDsJSON, &t.NodeIDs); err != nil {
		return nil, fmt.Errorf("unmarshal node_ids: %w", err)
	}
	if err := unmarshalJSON(resultsJSON, &t.Results); err != nil {
		return nil, fmt.Errorf("unmarshal results: %w", err)
	}
	return &t, nil
}

const taskSelectCols = `id, type, status, node_ids_json, results_json, created_at_unix, updated_at_unix`

func (s *Store) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskSelectCols+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.db.Query(`SELECT ` + taskSelectCols + ` FROM tasks ORDER BY created_at_unix DESC`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks scan: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Task{}
	}
	return out, nil
}

// --- Settings ---

func (s *Store) GetSettings() (*Settings, error) {
	var st Settings
	err := s.db.QueryRow(`
		SELECT admin_password_hash, default_agent_token, grpc_timeout_sec, max_concurrency, listen_addr, public_base_url
		FROM settings WHERE id = 1`).Scan(
		&st.AdminPasswordHash, &st.DefaultAgentToken, &st.GRPCTimeoutSec, &st.MaxConcurrency, &st.ListenAddr, &st.PublicBaseURL,
	)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	return &st, nil
}

func (s *Store) SaveSettings(st *Settings) error {
	if st == nil {
		return fmt.Errorf("settings is nil")
	}
	_, err := s.db.Exec(`
		UPDATE settings SET
			admin_password_hash = ?,
			default_agent_token = ?,
			grpc_timeout_sec = ?,
			max_concurrency = ?,
			listen_addr = ?,
			public_base_url = ?
		WHERE id = 1`,
		st.AdminPasswordHash, st.DefaultAgentToken, st.GRPCTimeoutSec, st.MaxConcurrency, st.ListenAddr, st.PublicBaseURL,
	)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// --- Subscriptions ---

func (s *Store) CreateSubscription(sub *Subscription) error {
	if sub == nil {
		return fmt.Errorf("subscription is nil")
	}
	if sub.ID == "" {
		sub.ID = newID()
	}
	if sub.Token == "" {
		return fmt.Errorf("token required")
	}
	now := nowUnix()
	if sub.CreatedAtUnix == 0 {
		sub.CreatedAtUnix = now
	}
	sub.UpdatedAtUnix = now
	if sub.InboundIDs == nil {
		sub.InboundIDs = []string{}
	}
	idsJSON, err := marshalJSON(sub.InboundIDs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO subscriptions (id, name, format, token, inbound_ids_json, enabled, created_at_unix, updated_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sub.ID, sub.Name, sub.Format, sub.Token, idsJSON, boolToInt(sub.Enabled), sub.CreatedAtUnix, sub.UpdatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}
	return nil
}

func (s *Store) UpdateSubscription(sub *Subscription) error {
	if sub == nil || sub.ID == "" {
		return fmt.Errorf("subscription id required")
	}
	sub.UpdatedAtUnix = nowUnix()
	if sub.InboundIDs == nil {
		sub.InboundIDs = []string{}
	}
	idsJSON, err := marshalJSON(sub.InboundIDs)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`
		UPDATE subscriptions SET name=?, format=?, token=?, inbound_ids_json=?, enabled=?, updated_at_unix=?
		WHERE id=?`,
		sub.Name, sub.Format, sub.Token, idsJSON, boolToInt(sub.Enabled), sub.UpdatedAtUnix, sub.ID,
	)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("subscription not found: %s", sub.ID)
	}
	return nil
}

func (s *Store) DeleteSubscription(id string) error {
	res, err := s.db.Exec(`DELETE FROM subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("subscription not found: %s", id)
	}
	return nil
}

func (s *Store) GetSubscription(id string) (*Subscription, error) {
	row := s.db.QueryRow(`
		SELECT id, name, format, token, inbound_ids_json, enabled, created_at_unix, updated_at_unix
		FROM subscriptions WHERE id = ?`, id)
	return scanSubscription(row)
}

func (s *Store) GetSubscriptionByToken(token string) (*Subscription, error) {
	row := s.db.QueryRow(`
		SELECT id, name, format, token, inbound_ids_json, enabled, created_at_unix, updated_at_unix
		FROM subscriptions WHERE token = ?`, token)
	sub, err := scanSubscription(row)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *Store) ListSubscriptions() ([]Subscription, error) {
	rows, err := s.db.Query(`
		SELECT id, name, format, token, inbound_ids_json, enabled, created_at_unix, updated_at_unix
		FROM subscriptions ORDER BY created_at_unix ASC`)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	if out == nil {
		out = []Subscription{}
	}
	return out, rows.Err()
}

func scanSubscription(row interface {
	Scan(dest ...any) error
}) (*Subscription, error) {
	var sub Subscription
	var idsJSON string
	var enabled int
	err := row.Scan(&sub.ID, &sub.Name, &sub.Format, &sub.Token, &idsJSON, &enabled, &sub.CreatedAtUnix, &sub.UpdatedAtUnix)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("subscription not found")
	}
	if err != nil {
		return nil, err
	}
	sub.Enabled = enabled != 0
	sub.InboundIDs = []string{}
	if err := unmarshalJSON(idsJSON, &sub.InboundIDs); err != nil {
		return nil, fmt.Errorf("unmarshal inbound_ids: %w", err)
	}
	return &sub, nil
}

// --- Snapshots ---

func (s *Store) SaveSnapshot(snap *ConfigSnapshot) error {
	if snap == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if snap.ID == "" {
		snap.ID = newID()
	}
	if snap.CreatedAtUnix == 0 {
		snap.CreatedAtUnix = nowUnix()
	}
	_, err := s.db.Exec(`
		INSERT INTO config_snapshots (id, node_id, config_json, config_hash, task_id, created_at_unix)
		VALUES (?, ?, ?, ?, ?, ?)`,
		snap.ID, snap.NodeID, snap.ConfigJSON, snap.ConfigHash, snap.TaskID, snap.CreatedAtUnix,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (s *Store) LatestSnapshot(nodeID string) (*ConfigSnapshot, error) {
	var snap ConfigSnapshot
	err := s.db.QueryRow(`
		SELECT id, node_id, config_json, config_hash, task_id, created_at_unix
		FROM config_snapshots
		WHERE node_id = ?
		ORDER BY created_at_unix DESC
		LIMIT 1`, nodeID).Scan(
		&snap.ID, &snap.NodeID, &snap.ConfigJSON, &snap.ConfigHash, &snap.TaskID, &snap.CreatedAtUnix,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("snapshot not found for node: %s", nodeID)
	}
	if err != nil {
		return nil, fmt.Errorf("latest snapshot: %w", err)
	}
	return &snap, nil
}
