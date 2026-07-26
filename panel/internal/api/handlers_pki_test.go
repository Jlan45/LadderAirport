package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/pki"
	"github.com/ladderairport/panel/internal/store"
)

func TestIssueAgentCertificateBindsNode(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{
		Name: "edge", Token: "node-secret", GRPCPort: 50051, Status: "pending",
		PKIMigrationRequired: true,
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	enrollmentToken, err := st.CreatePKIEnrollmentToken(node.ID, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.Open(filepath.Join(t.TempDir(), "pki"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&Server{Store: st, Secret: []byte("test-secret"), PKI: ca}).Handler())
	defer server.Close()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "ignored"},
		DNSNames: []string{"edge.example.test"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"node_id":   node.ID,
		"csr_pem":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		"address":   "edge.example.test",
		"grpc_port": 50051,
	})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/pki/agent-certificates", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+enrollmentToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var issued issueAgentCertificateResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if issued.Serial == "" || issued.ManagementID != pki.AgentURI(node.ID) {
		t.Fatalf("unexpected issued response: %+v", issued)
	}
	if issued.ControlToken != "node-secret" {
		t.Fatalf("control token = %q", issued.ControlToken)
	}
	got, err := st.GetNode(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PKICertSerial != issued.Serial || got.PKICABundlePEM == "" || !got.PKIMigrationRequired {
		t.Fatalf("node not bound to issued certificate: %+v", got)
	}
	if got.Address != "edge.example.test" {
		t.Fatalf("address = %q", got.Address)
	}
	completeBody, _ := json.Marshal(map[string]string{"node_id": node.ID, "serial": issued.Serial})
	completeReq, _ := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/v1/pki/agent-migrations/complete",
		bytes.NewReader(completeBody),
	)
	completeReq.Header.Set("Authorization", "Bearer node-secret")
	completeReq.Header.Set("Content-Type", "application/json")
	completeResp, err := http.DefaultClient.Do(completeReq)
	if err != nil {
		t.Fatal(err)
	}
	completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete migration status = %d", completeResp.StatusCode)
	}
	got, err = st.GetNode(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PKIMigrationRequired {
		t.Fatalf("migration marker not cleared: %+v", got)
	}

	replay, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/pki/agent-certificates", bytes.NewReader(body))
	replay.Header.Set("Authorization", "Bearer "+enrollmentToken)
	replay.Header.Set("Content-Type", "application/json")
	replayResp, err := http.DefaultClient.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed enrollment token status = %d", replayResp.StatusCode)
	}
	if err := st.RevokePKICertificate(issued.Serial, "test"); err != nil {
		t.Fatal(err)
	}
	revokedNode, err := st.GetNode(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revokedNode.Token != "" || revokedNode.PKICertSerial != "" {
		t.Fatalf("revocation did not clear node credentials: %+v", revokedNode)
	}
}

func TestIssueAgentCertificateRejectsWrongToken(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Token: "right", GRPCPort: 50051}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	ca, _ := pki.Open(filepath.Join(t.TempDir(), "pki"))
	server := httptest.NewServer((&Server{Store: st, Secret: []byte("test-secret"), PKI: ca}).Handler())
	defer server.Close()
	body, _ := json.Marshal(map[string]string{"node_id": node.ID, "csr_pem": "bad"})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/pki/agent-certificates", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
