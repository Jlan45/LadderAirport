package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func openAuthTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEnsureAdminPasswordGeneratesRandom(t *testing.T) {
	// Isolate from an operator-provided preset.
	t.Setenv("LADDER_ADMIN_PASSWORD", "")
	st := openAuthTestStore(t)
	if err := EnsureAdminPassword(st); err != nil {
		t.Fatal(err)
	}
	settings, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminPasswordHash == "" {
		t.Fatal("password hash not persisted")
	}
	// The legacy default "admin" must never authenticate a fresh install.
	if CheckPassword(settings.AdminPasswordHash, "admin") {
		t.Fatal("generated password must not be the legacy default")
	}
	// Idempotent: a second call keeps the existing hash.
	if err := EnsureAdminPassword(st); err != nil {
		t.Fatal(err)
	}
	again, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.AdminPasswordHash != settings.AdminPasswordHash {
		t.Fatal("EnsureAdminPassword must not rotate an existing password")
	}
}

func TestEnsureAdminPasswordFromEnv(t *testing.T) {
	st := openAuthTestStore(t)
	t.Setenv("LADDER_ADMIN_PASSWORD", "preset-e2e-secret")
	if err := EnsureAdminPassword(st); err != nil {
		t.Fatal(err)
	}
	settings, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(settings.AdminPasswordHash, "preset-e2e-secret") {
		t.Fatal("env-provided initial password must authenticate")
	}
	// An existing hash wins over the environment variable.
	t.Setenv("LADDER_ADMIN_PASSWORD", "rotated-secret")
	if err := EnsureAdminPassword(st); err != nil {
		t.Fatal(err)
	}
	again, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.AdminPasswordHash != settings.AdminPasswordHash {
		t.Fatal("env must not override an existing password hash")
	}
}

func TestLoginBackoffStateMachine(t *testing.T) {
	st := openAuthTestStore(t)
	s := &Server{Store: st}
	ip := "203.0.113.9"

	for i := 0; i < loginMaxFailures-1; i++ {
		s.recordLoginFailure(ip)
		if remaining := s.loginLockedFor(ip); remaining != 0 {
			t.Fatalf("locked after %d failures: %s", i+1, remaining)
		}
	}
	s.recordLoginFailure(ip)
	if remaining := s.loginLockedFor(ip); remaining <= 0 {
		t.Fatal("not locked after reaching failure threshold")
	}
	s.clearLoginFailures(ip)
	if remaining := s.loginLockedFor(ip); remaining != 0 {
		t.Fatalf("still locked after success: %s", remaining)
	}
}

func TestRequestSchemeTrustedProxy(t *testing.T) {
	st := openAuthTestStore(t)
	settings, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.TrustedProxyCIDRs = "10.0.0.0/8"
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st}

	newReq := func(remoteAddr, xfp string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://panel.example/sub", nil)
		r.RemoteAddr = remoteAddr
		if xfp != "" {
			r.Header.Set("X-Forwarded-Proto", xfp)
		}
		return r
	}

	// Trusted proxy: header honored.
	if got := s.requestScheme(newReq("10.1.2.3:8443", "https")); got != "https" {
		t.Fatalf("trusted proxy scheme = %s", got)
	}
	// Untrusted peer: header ignored.
	if got := s.requestScheme(newReq("192.0.2.1:8443", "https")); got != "http" {
		t.Fatalf("untrusted peer scheme = %s", got)
	}
	// Direct TLS always wins.
	r := newReq("192.0.2.1:8443", "")
	r.TLS = &tls.ConnectionState{}
	if got := s.requestScheme(r); got != "https" {
		t.Fatalf("direct TLS scheme = %s", got)
	}
}

func TestRequestSchemeNoTrustedProxies(t *testing.T) {
	st := openAuthTestStore(t)
	s := &Server{Store: st}
	r := httptest.NewRequest(http.MethodGet, "http://panel.example/sub", nil)
	r.RemoteAddr = "10.1.2.3:8443"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := s.requestScheme(r); got != "http" {
		t.Fatalf("default-trust scheme = %s", got)
	}
}

func TestNormalizeCIDRList(t *testing.T) {
	got, err := normalizeCIDRList(" 10.0.0.0/8 , 192.168.0.0/16,,")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.0/8,192.168.0.0/16" {
		t.Fatalf("got %q", got)
	}
	if empty, err := normalizeCIDRList("  "); err != nil || empty != "" {
		t.Fatalf("empty: %q err=%v", empty, err)
	}
	if _, err := normalizeCIDRList("not-a-cidr"); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestBumpSessionVersion(t *testing.T) {
	st := openAuthTestStore(t)
	before, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	v1, err := st.BumpSessionVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v1 != before.SessionVersion+1 {
		t.Fatalf("v1=%d before=%d", v1, before.SessionVersion)
	}
	v2, err := st.BumpSessionVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v2 != v1+1 {
		t.Fatalf("v2=%d v1=%d", v2, v1)
	}
	// SaveSettings must not clobber the bumped version.
	settings, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ListenAddr = ":9999"
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	after, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if after.SessionVersion != v2 {
		t.Fatalf("SaveSettings clobbered session version: %d", after.SessionVersion)
	}
}
