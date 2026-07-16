package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsAgentVersionOutdated(t *testing.T) {
	cases := []struct {
		cur, rec string
		want     bool
	}{
		{"", "v0.3.1", true},
		{"0.1.0-dev", "v0.3.1", true},
		{"v0.3.1", "v0.3.1", false},
		{"0.3.1", "v0.3.1", false},
		{"v0.2.0", "v0.3.1", true},
		{"v0.4.0", "v0.3.1", false}, // newer than recommended
		{"v0.3.1", "", false},
		{"unknown", "v1.0.0", true},
		{"v0.3.1-rc.1", "v0.3.1", true},
		{"v0.3.1", "v0.3.1-rc.1", false},
	}
	for _, c := range cases {
		got := isAgentVersionOutdated(c.cur, c.rec)
		if got != c.want {
			t.Fatalf("isAgentVersionOutdated(%q,%q)=%v want %v", c.cur, c.rec, got, c.want)
		}
	}
}

func TestHandleGetMetaUsesReleaseAPI(t *testing.T) {
	// Fake GitHub latest release endpoint.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
	}))
	t.Cleanup(upstream.Close)

	releaseCacheMu.Lock()
	oldURL := releaseRepoAPIURL
	oldTag := releaseCacheTag
	oldAt := releaseCacheAt
	releaseRepoAPIURL = upstream.URL
	releaseCacheTag = ""
	releaseCacheAt = time.Time{}
	releaseCacheMu.Unlock()
	t.Cleanup(func() {
		releaseCacheMu.Lock()
		releaseRepoAPIURL = oldURL
		releaseCacheTag = oldTag
		releaseCacheAt = oldAt
		releaseCacheMu.Unlock()
	})

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	rr := httptest.NewRecorder()
	s.handleGetMeta(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var meta Meta
	if err := json.Unmarshal(rr.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.RecommendedAgentVersion != "v9.9.9" {
		t.Fatalf("tag = %q", meta.RecommendedAgentVersion)
	}
	if meta.Source != "github" {
		t.Fatalf("source = %q", meta.Source)
	}
	if meta.PanelVersion == "" {
		t.Fatal("panel_version empty")
	}
	if meta.AgentUpgradeCommand == "" || !strings.Contains(meta.AgentUpgradeCommand, "LADDER_ACTION=upgrade") || !strings.Contains(meta.AgentUpgradeCommand, "v9.9.9") {
		t.Fatalf("upgrade cmd = %s", meta.AgentUpgradeCommand)
	}
	if meta.AgentUninstallCommand == "" || !strings.Contains(meta.AgentUninstallCommand, "LADDER_ACTION=uninstall") {
		t.Fatalf("uninstall cmd = %s", meta.AgentUninstallCommand)
	}
}
