package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

func TestUplinkEnrollSkipsCertificate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{
		Name: "edge", Token: "node-secret", Status: "pending", ControlMode: store.ControlModeUplink,
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	enrollmentToken, err := st.CreatePKIEnrollmentToken(node.ID, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&Server{Store: st, Secret: []byte("test-secret")}).Handler())
	defer server.Close()

	body, _ := json.Marshal(map[string]string{"node_id": node.ID})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/enroll", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+enrollmentToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var issued enrollAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if issued.ControlToken != "node-secret" {
		t.Fatalf("control token = %q", issued.ControlToken)
	}
	enrolled, err := st.IsAgentEnrolled(node.ID)
	if err != nil || !enrolled {
		t.Fatalf("enrolled=%v err=%v", enrolled, err)
	}
	got, err := st.GetNode(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PKICertSerial != "" || got.Status != "unknown" {
		t.Fatalf("node after enroll = %+v", got)
	}

	replay, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/enroll", bytes.NewReader(body))
	replay.Header.Set("Authorization", "Bearer "+enrollmentToken)
	replay.Header.Set("Content-Type", "application/json")
	replayResp, err := http.DefaultClient.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay status = %d", replayResp.StatusCode)
	}
}

func TestPushNodeCannotSkipTLSEnrollment(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Token: "node-secret", GRPCPort: 50051}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	enrollmentToken, err := st.CreatePKIEnrollmentToken(node.ID, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&Server{Store: st, Secret: []byte("test-secret")}).Handler())
	defer server.Close()

	body, _ := json.Marshal(map[string]string{"node_id": node.ID})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/enroll", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+enrollmentToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	ok, err := st.ConsumePKIEnrollmentToken(node.ID, enrollmentToken)
	if err != nil || !ok {
		t.Fatalf("enrollment token consumed early: ok=%v err=%v", ok, err)
	}
}

func TestUplinkWithoutCertCanSwitchBeforeEnroll(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Token: "node-secret", ControlMode: store.ControlModeUplink, Status: "pending"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	enrolled, err := st.IsAgentEnrolled(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled {
		t.Fatal("new uplink node should not count as enrolled")
	}
	if err := st.MarkAgentEnrolled(node.ID); err != nil {
		t.Fatal(err)
	}
	enrolled, err = st.IsAgentEnrolled(node.ID)
	if err != nil || !enrolled {
		t.Fatalf("enrolled=%v err=%v", enrolled, err)
	}
}

func TestUplinkRegistrationGate(t *testing.T) {
	pending := store.Node{ControlMode: store.ControlModeUplink}
	if !nodeAwaitingRegistration(pending, false) {
		t.Fatal("new uplink node should still need registration")
	}
	if nodeAwaitingRegistration(pending, true) {
		t.Fatal("enrolled uplink node should not need another install token")
	}
	push := store.Node{ControlMode: store.ControlModePush}
	if !nodeAwaitingRegistration(push, false) {
		t.Fatal("push node without a certificate should need registration")
	}
	push.PKICertSerial = "abc"
	if nodeAwaitingRegistration(push, false) {
		t.Fatal("push node with a certificate is already registered")
	}
}
