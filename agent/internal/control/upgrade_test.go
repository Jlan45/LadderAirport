package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestStageAgentUpgradeFromDirectURL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("upgrade staging is linux-only")
	}
	payload := []byte("#!/bin/sh\necho fake-agent\n")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/bin") {
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	res, err := StageAgentUpgrade(context.Background(), UpgradeRequest{
		Version:     "v9.9.9",
		DownloadURL: srv.URL + "/bin",
		SHA256:      hexSum,
		UpgradeDir:  dir,
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("StageAgentUpgrade: %v", err)
	}
	if res.Version != "v9.9.9" {
		t.Fatalf("version = %q", res.Version)
	}
	if _, err := os.Stat(res.StagedPath); err != nil {
		t.Fatalf("staged missing: %v", err)
	}
	ready := res.StagedPath + ".ready"
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("ready marker missing: %v", err)
	}
	got, err := os.ReadFile(res.StagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestStageAgentUpgradeSHAMismatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("upgrade staging is linux-only")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	t.Cleanup(srv.Close)

	_, err := StageAgentUpgrade(context.Background(), UpgradeRequest{
		DownloadURL: srv.URL,
		SHA256:      strings.Repeat("0", 64),
		UpgradeDir:  t.TempDir(),
		HTTPClient:  srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha mismatch, got %v", err)
	}
}
