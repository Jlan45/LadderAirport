package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/version"
	"github.com/ladderairport/pkg/semver"
)

// Meta is panel/fleet metadata useful for the SPA (versions, upgrade hints).
type Meta struct {
	// PanelVersion is this panel binary version (ldflags).
	PanelVersion string `json:"panel_version"`
	// PanelCommit is the build commit when injected.
	PanelCommit string `json:"panel_commit,omitempty"`
	// RecommendedAgentVersion is the latest GitHub Release tag when reachable (e.g. "v0.3.1").
	// Empty when unknown (offline / private repo / rate limit).
	RecommendedAgentVersion string `json:"recommended_agent_version,omitempty"`
	// AgentUpgradeCommand is a generic curl|bash upgrade one-liner (latest or pinned).
	AgentUpgradeCommand string `json:"agent_upgrade_command"`
	// AgentUninstallCommand stops service and removes binary/unit (keeps conf/data).
	AgentUninstallCommand string `json:"agent_uninstall_command"`
	// Source describes where RecommendedAgentVersion came from: "github" | "none".
	Source string `json:"source"`
	// CheckedAtUnix is when we last resolved the recommended version (0 if never).
	CheckedAtUnix int64 `json:"checked_at_unix,omitempty"`
}

const githubLatestReleaseURL = "https://api.github.com/repos/Jlan45/LadderAirport/releases/latest"

var (
	releaseCacheMu     sync.Mutex
	releaseCacheTag    string
	releaseCacheAt     time.Time
	releaseCacheTTL    = 15 * time.Minute
	releaseHTTPClient  = &http.Client{Timeout: 4 * time.Second}
	releaseRepoAPIURL  = githubLatestReleaseURL // overridable in tests
)

// handleGetMeta returns version / upgrade hints for the SPA.
// GET /api/v1/meta
func (s *Server) handleGetMeta(w http.ResponseWriter, r *http.Request) {
	tag, src, checked := resolveRecommendedAgentVersion()
	writeJSON(w, http.StatusOK, Meta{
		PanelVersion:            version.Version,
		PanelCommit:             version.Commit,
		RecommendedAgentVersion: tag,
		AgentUpgradeCommand: buildUpgradeCommand(installCommandOpts{
			AgentVersion: tag,
		}),
		AgentUninstallCommand: buildUninstallCommand(installCommandOpts{}, false),
		Source:                src,
		CheckedAtUnix:         checked,
	})
}

func resolveRecommendedAgentVersion() (tag, source string, checkedUnix int64) {
	releaseCacheMu.Lock()
	defer releaseCacheMu.Unlock()

	if releaseCacheTag != "" && time.Since(releaseCacheAt) < releaseCacheTTL {
		return releaseCacheTag, "github", releaseCacheAt.Unix()
	}

	tag, err := fetchLatestReleaseTag()
	if err != nil || tag == "" {
		// Keep stale cache if any; otherwise report none.
		if releaseCacheTag != "" {
			return releaseCacheTag, "github", releaseCacheAt.Unix()
		}
		return "", "none", 0
	}
	releaseCacheTag = tag
	releaseCacheAt = time.Now()
	return tag, "github", releaseCacheAt.Unix()
}

func fetchLatestReleaseTag() (string, error) {
	req, err := http.NewRequest(http.MethodGet, releaseRepoAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LadderAirport-Panel")

	resp, err := releaseHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(parsed.TagName)
	if tag == "" {
		return "", fmt.Errorf("empty tag_name")
	}
	return tag, nil
}

// isAgentVersionOutdated reports whether current should be upgraded to recommended
// using semantic version comparison (see pkg/semver).
func isAgentVersionOutdated(current, recommended string) bool {
	return semver.IsOutdated(current, recommended)
}
