package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func tokenHashForTest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestPruneExpiredData(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	old := now.AddDate(0, 0, -40).Unix()
	oldAudit := now.AddDate(0, 0, -100).Unix()

	node := &Node{Name: "edge", Address: "192.0.2.1", GRPCPort: 50051}
	if err := s.CreateNode(node); err != nil {
		t.Fatal(err)
	}

	// Tasks: one recent, one older than 30 days.
	recent := &Task{Type: "apply", Status: "success", NodeIDs: []string{node.ID}}
	if err := s.CreateTask(recent); err != nil {
		t.Fatal(err)
	}
	stale := &Task{Type: "apply", Status: "success", NodeIDs: []string{node.ID}}
	if err := s.CreateTask(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tasks SET created_at_unix = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatal(err)
	}

	// Snapshots: 25 on node A, 3 on node B — keep newest 20 / all 3.
	nodeB := &Node{Name: "edge-b", Address: "192.0.2.2", GRPCPort: 50051}
	if err := s.CreateNode(nodeB); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		snap := &ConfigSnapshot{NodeID: node.ID, ConfigJSON: "{}", ConfigHash: "h", CreatedAtUnix: now.Unix() - int64(i)}
		if err := s.SaveSnapshot(snap); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		snap := &ConfigSnapshot{NodeID: nodeB.ID, ConfigJSON: "{}", ConfigHash: "h"}
		if err := s.SaveSnapshot(snap); err != nil {
			t.Fatal(err)
		}
	}

	// Audit logs: one recent, one older than 90 days, in both tables.
	if err := s.AddPKIAudit("agent.certificate.issue", node.ID, "s1", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPKIAudit("certificate.revoke", node.ID, "s2", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE pki_audit_logs SET created_at_unix = ? WHERE serial = 's2'`, oldAudit); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAutomationAudit(&AutomationAuditLog{Action: "dns.reconcile", TargetType: "managed_domain"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAutomationAudit(&AutomationAuditLog{Action: "cert.issue", TargetType: "protocol_certificate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE automation_audit_logs SET created_at_unix = ? WHERE action = 'cert.issue'`, oldAudit); err != nil {
		t.Fatal(err)
	}

	// Enrollment tokens: one active, one used, one expired.
	activeToken, err := s.CreatePKIEnrollmentToken(node.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	usedToken, err := s.CreatePKIEnrollmentToken(node.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	expiredToken, err := s.CreatePKIEnrollmentToken(node.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mark := func(token string, used, expires int64) {
		t.Helper()
		hash := tokenHashForTest(token)
		if _, err := s.db.Exec(`UPDATE pki_enrollment_tokens SET used_at_unix = ?, expires_at_unix = ? WHERE token_hash = ?`, used, expires, hash); err != nil {
			t.Fatal(err)
		}
	}
	mark(activeToken, 0, now.Add(time.Hour).Unix())
	mark(usedToken, now.Unix(), now.Add(time.Hour).Unix())
	mark(expiredToken, 0, old)

	summary, err := s.PruneExpiredData(now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tasks != 1 || summary.Snapshots != 5 || summary.PKIAuditLogs != 1 ||
		summary.AutomationAuditLogs != 1 || summary.EnrollmentTokens != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	assertCount := func(table string, want int) {
		t.Helper()
		var got int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}
	assertCount("tasks", 1)
	assertCount("pki_audit_logs", 1)
	assertCount("automation_audit_logs", 1)
	assertCount("pki_enrollment_tokens", 1)
	var snapA, snapB int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM config_snapshots WHERE node_id = ?`, node.ID).Scan(&snapA); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM config_snapshots WHERE node_id = ?`, nodeB.ID).Scan(&snapB); err != nil {
		t.Fatal(err)
	}
	if snapA != SnapshotsPerNode || snapB != 3 {
		t.Fatalf("snapshots: nodeA=%d nodeB=%d", snapA, snapB)
	}
	// The kept snapshots must be the newest ones.
	latest, err := s.LatestSnapshot(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.CreatedAtUnix != now.Unix() {
		t.Fatalf("latest snapshot created_at = %d", latest.CreatedAtUnix)
	}
}

func TestNodeConfigFingerprintChanges(t *testing.T) {
	s := openTestStore(t)
	node := &Node{Name: "edge", Address: "192.0.2.1", GRPCPort: 50051}
	if err := s.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	inbound := &InboundConfig{
		Name: "trojan", Protocol: "trojan", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 443, "password": "secret"},
	}
	if err := s.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	// updated_at_unix has 1-second resolution; age the row so the later
	// UpdateInbound is guaranteed a newer timestamp.
	if _, err := s.db.Exec(`UPDATE inbounds SET updated_at_unix = updated_at_unix - 100 WHERE id = ?`, inbound.ID); err != nil {
		t.Fatal(err)
	}

	fp1, err := s.NodeConfigFingerprint(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Unrelated rows must not move the fingerprint.
	if err := s.CreateTask(&Task{Type: "apply", Status: "pending", NodeIDs: []string{node.ID}}); err != nil {
		t.Fatal(err)
	}
	fp2, err := s.NodeConfigFingerprint(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint moved by unrelated task: %q -> %q", fp1, fp2)
	}
	// An inbound param change must move it.
	inbound.Params["password"] = "rotated"
	if err := s.UpdateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	fp3, err := s.NodeConfigFingerprint(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fp3 == fp2 {
		t.Fatal("fingerprint did not change after inbound update")
	}
}

func TestListAllNodeInboundAttachments(t *testing.T) {
	s := openTestStore(t)
	nodeA := &Node{Name: "a", Address: "192.0.2.1", GRPCPort: 50051}
	nodeB := &Node{Name: "b", Address: "192.0.2.2", GRPCPort: 50051}
	for _, n := range []*Node{nodeA, nodeB} {
		if err := s.CreateNode(n); err != nil {
			t.Fatal(err)
		}
	}
	inbound := &InboundConfig{
		Name: "trojan", Protocol: "trojan", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 443, "password": "secret"},
	}
	if err := s.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeInboundBindings(nodeA.ID, []NodeInboundBinding{{InboundID: inbound.ID, PublicPort: 8443}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeInboundBindings(nodeB.ID, []NodeInboundBinding{{InboundID: inbound.ID}}); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListAllNodeInboundAttachments()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("groups = %d", len(all))
	}
	perNode, err := s.ListNodeInboundAttachments(nodeA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all[nodeA.ID]) != len(perNode) || all[nodeA.ID][0].ID != perNode[0].ID ||
		all[nodeA.ID][0].PublicPort != perNode[0].PublicPort {
		t.Fatalf("batch = %+v vs per-node = %+v", all[nodeA.ID], perNode)
	}
	if all[nodeA.ID][0].PublicPort != 8443 || all[nodeB.ID][0].PublicPort != 0 {
		t.Fatalf("public ports wrong: %+v", all)
	}
}
