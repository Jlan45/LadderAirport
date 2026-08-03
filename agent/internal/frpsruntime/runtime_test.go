package frpsruntime

import (
	"context"
	"net"
	"testing"
)

func TestRuntimeApplyStopAndRestore(t *testing.T) {
	port := freeTCPPort(t)
	proxyPort := freeTCPPort(t)
	for proxyPort == port {
		proxyPort = freeTCPPort(t)
	}
	config := Config{
		Enabled:       true,
		BindAddr:      "127.0.0.1",
		BindPort:      port,
		ProxyBindAddr: "127.0.0.1",
		AllowPorts:    []PortRange{{Start: proxyPort}},
		AuthToken:     "test-token",
		TLSForce:      true,
	}

	dataDir := t.TempDir()
	runtime := New(dataDir)
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	if err := runtime.Apply(context.Background(), config, "hash-1"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if status := runtime.Status(context.Background()); status.State != StateRunning {
		t.Fatalf("state = %s, want running; error=%s", status.State, status.LastError)
	}
	snapshot, err := runtime.Mappings(context.Background())
	if err != nil {
		t.Fatalf("Mappings: %v", err)
	}
	if len(snapshot.Clients) != 0 || len(snapshot.Mappings) != 0 {
		t.Fatalf("unexpected empty-runtime mappings: %+v", snapshot)
	}
	if snapshot.CollectedAtUnix == 0 {
		t.Fatal("Mappings collected_at_unix is empty")
	}
	if err := runtime.Apply(context.Background(), config, "hash-1"); err != nil {
		t.Fatalf("idempotent Apply: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status := runtime.Status(context.Background()); status.State != StateStopped {
		t.Fatalf("state after stop = %s, want stopped", status.State)
	}

	restored := New(dataDir)
	t.Cleanup(func() { _ = restored.Stop(context.Background()) })
	if err := restored.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if status := restored.Status(context.Background()); status.State != StateRunning {
		t.Fatalf("restored state = %s, want running; error=%s", status.State, status.LastError)
	} else if status.ConfigHash != "hash-1" {
		t.Fatalf("restored hash = %q, want hash-1", status.ConfigHash)
	}
}

func TestRuntimeFailedApplyRestoresPreviousConfig(t *testing.T) {
	port := freeTCPPort(t)
	proxyPort := freeTCPPort(t)
	config := Config{
		Enabled:       true,
		BindAddr:      "127.0.0.1",
		BindPort:      port,
		ProxyBindAddr: "127.0.0.1",
		AllowPorts:    []PortRange{{Start: proxyPort}},
		AuthToken:     "test-token",
		TLSForce:      true,
	}
	runtime := New(t.TempDir())
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if err := runtime.Apply(context.Background(), config, "old"); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	blockedPort := blocker.Addr().(*net.TCPAddr).Port
	candidate := config
	candidate.BindPort = blockedPort
	if err := runtime.Apply(context.Background(), candidate, "new"); err == nil {
		t.Fatal("Apply unexpectedly succeeded")
	}
	status := runtime.Status(context.Background())
	if status.State != StateRunning || status.ConfigHash != "old" {
		t.Fatalf("status after rollback = %+v", status)
	}
}

func TestConfigAcceptsPrivilegedPorts(t *testing.T) {
	config := Config{
		Enabled:       true,
		BindAddr:      "127.0.0.1",
		BindPort:      80,
		ProxyBindAddr: "127.0.0.1",
		AllowPorts:    []PortRange{{Start: 443, End: 443}},
		AuthToken:     "secret",
		TLSForce:      true,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("privileged ports 80/443 should pass validation: %v", err)
	}
	config.BindPort = 1
	config.AllowPorts = []PortRange{{Start: 1023, End: 1023}}
	if err := config.Validate(); err != nil {
		t.Fatalf("boundary ports 1/1023 should pass validation: %v", err)
	}
}

func TestConfigRejectsOutOfRangeAndUnboundedPorts(t *testing.T) {
	base := Config{
		Enabled:       true,
		BindAddr:      "127.0.0.1",
		BindPort:      7000,
		ProxyBindAddr: "127.0.0.1",
		AllowPorts:    []PortRange{{Start: 20000, End: 20100}},
		AuthToken:     "secret",
		TLSForce:      true,
	}
	for _, bindPort := range []int{-1, 65536} {
		config := base
		config.BindPort = bindPort
		if err := config.Validate(); err == nil {
			t.Fatalf("bind port %d should fail validation", bindPort)
		}
	}
	for _, portRange := range []PortRange{{Start: 0, End: 100}, {Start: 20000, End: 65536}} {
		config := base
		config.AllowPorts = []PortRange{portRange}
		if err := config.Validate(); err == nil {
			t.Fatalf("allow_ports %+v should fail validation", portRange)
		}
	}
	config := base
	config.AllowPorts = nil
	if err := config.Validate(); err == nil {
		t.Fatal("empty allow_ports should fail validation")
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if port < 1024 {
		return freeTCPPort(t)
	}
	return port
}
