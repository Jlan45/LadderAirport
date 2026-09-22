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

func TestAgentReportAndConfigSyncReuseHTTPBearer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{
		Name:        "edge",
		Token:       "node-secret",
		ControlMode: store.ControlModeUplink,
		Status:      "pending",
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&Server{Store: st, Secret: []byte("test-secret")}).Handler())
	defer server.Close()

	reportBody, _ := json.Marshal(map[string]any{
		"node_id":           node.ID,
		"collected_at_unix": time.Now().Unix(),
		"runtime_state":     "running",
		"config_hash":       "cfg-1",
		"agent_version":     "v-test",
		"capabilities":      []string{"uplink-v1"},
		"connections":       2,
		"uplink_bytes":      10,
		"downlink_bytes":    20,
		"cpu_percent":       3.5,
		"memory_rss_bytes":  4096,
	})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/report", bytes.NewReader(reportBody))
	req.Header.Set("Authorization", "Bearer node-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report status = %d", resp.StatusCode)
	}

	got, err := st.GetNode(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "online" || got.RuntimeState != "running" || got.ConfigHash != "cfg-1" {
		t.Fatalf("report not applied: %+v", got)
	}
	if got.UplinkLastSeenUnix == 0 || got.Connections != 2 {
		t.Fatalf("metrics not applied: %+v", got)
	}

	wrong, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/report", bytes.NewReader(reportBody))
	wrong.Header.Set("Authorization", "Bearer other")
	wrong.Header.Set("Content-Type", "application/json")
	wrongResp, err := http.DefaultClient.Do(wrong)
	if err != nil {
		t.Fatal(err)
	}
	defer wrongResp.Body.Close()
	if wrongResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", wrongResp.StatusCode)
	}

	syncBody, _ := json.Marshal(map[string]any{
		"node_id":             node.ID,
		"applied_config_hash": "",
		"applied_frps_hash":   "",
	})
	syncReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/config-sync", bytes.NewReader(syncBody))
	syncReq.Header.Set("Authorization", "Bearer node-secret")
	syncReq.Header.Set("Content-Type", "application/json")
	syncResp, err := http.DefaultClient.Do(syncReq)
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("config-sync status = %d", syncResp.StatusCode)
	}
	var first agentConfigSyncResponse
	if err := json.NewDecoder(syncResp.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.ConfigJSON == "" || first.ConfigHash == "" || first.DesiredState != store.DesiredRuntimeRunning {
		t.Fatalf("first sync = %+v", first)
	}

	againBody, _ := json.Marshal(map[string]any{
		"node_id":             node.ID,
		"applied_config_hash": first.ConfigHash,
		"applied_frps_hash":   first.FRPSHash,
	})
	againReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent/config-sync", bytes.NewReader(againBody))
	againReq.Header.Set("Authorization", "Bearer node-secret")
	againReq.Header.Set("Content-Type", "application/json")
	againResp, err := http.DefaultClient.Do(againReq)
	if err != nil {
		t.Fatal(err)
	}
	defer againResp.Body.Close()
	if againResp.StatusCode != http.StatusOK {
		t.Fatalf("unchanged sync status = %d", againResp.StatusCode)
	}
	var second agentConfigSyncResponse
	if err := json.NewDecoder(againResp.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.ConfigJSON != "" || second.DesiredState != store.DesiredRuntimeRunning {
		t.Fatalf("unchanged sync = %+v", second)
	}

	headReq, _ := http.NewRequest(http.MethodHead, server.URL+"/api/v1/agent/config-sync?node_id="+node.ID, nil)
	headReq.Header.Set("Authorization", "Bearer node-secret")
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	defer headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", headResp.StatusCode)
	}
	if headResp.Header.Get(headerConfigHash) != first.ConfigHash {
		t.Fatalf("HEAD hash = %q, want %q", headResp.Header.Get(headerConfigHash), first.ConfigHash)
	}
	if headResp.Header.Get(headerDesiredState) != store.DesiredRuntimeRunning {
		t.Fatalf("HEAD desired = %q", headResp.Header.Get(headerDesiredState))
	}

	getReq, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/agent/config-sync?node_id="+node.ID, nil)
	getReq.Header.Set("Authorization", "Bearer node-secret")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", getResp.StatusCode)
	}
	var meta agentConfigHeadResponse
	if err := json.NewDecoder(getResp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.ConfigHash != first.ConfigHash || meta.DesiredState != store.DesiredRuntimeRunning {
		t.Fatalf("GET meta = %+v", meta)
	}

	wrongHead, _ := http.NewRequest(http.MethodHead, server.URL+"/api/v1/agent/config-sync?node_id="+node.ID, nil)
	wrongHead.Header.Set("Authorization", "Bearer other")
	wrongHeadResp, err := http.DefaultClient.Do(wrongHead)
	if err != nil {
		t.Fatal(err)
	}
	defer wrongHeadResp.Body.Close()
	if wrongHeadResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong HEAD token status = %d", wrongHeadResp.StatusCode)
	}
}
