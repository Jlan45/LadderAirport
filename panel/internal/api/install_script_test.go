package api

import (
	"strings"
	"testing"
)

func TestBuildInstallCommandTLS(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		Token: "tok'en", AgentVersion: "latest", EnableTLS: true,
	})
	if !strings.Contains(cmd, "LADDER_TLS=1") {
		t.Fatalf("want TLS=1: %s", cmd)
	}
	if !strings.Contains(cmd, "LADDER_TOKEN=") {
		t.Fatalf("missing token: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_VERSION=") {
		t.Fatalf("latest should omit version: %s", cmd)
	}
	if !strings.Contains(cmd, defaultInstallScriptURL) {
		t.Fatalf("missing script url: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_PANEL=") {
		t.Fatalf("no panel without base url: %s", cmd)
	}
}

func TestBuildInstallCommandWithEnroll(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		Token: "abc", EnableTLS: true,
		PanelBaseURL: "https://panel.example.com/",
		NodeID:       "nid-1",
		GRPCPort:     50051,
	})
	if !strings.Contains(cmd, "LADDER_PANEL='https://panel.example.com'") {
		t.Fatalf("panel: %s", cmd)
	}
	if !strings.Contains(cmd, "LADDER_NODE_ID='nid-1'") {
		t.Fatalf("node id: %s", cmd)
	}
	if !strings.Contains(cmd, "LADDER_GRPC_PORT=50051") {
		t.Fatalf("port: %s", cmd)
	}
}

func TestBuildInstallCommandPlainVersion(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		Token: "abc", AgentVersion: "v0.2.0", EnableTLS: false,
	})
	if !strings.Contains(cmd, "LADDER_TLS=0") {
		t.Fatalf("%s", cmd)
	}
	if !strings.Contains(cmd, "LADDER_VERSION='v0.2.0'") {
		t.Fatalf("%s", cmd)
	}
}

func TestRandomAgentToken(t *testing.T) {
	a, err := randomAgentToken()
	if err != nil || len(a) < 32 {
		t.Fatalf("token=%q err=%v", a, err)
	}
	b, _ := randomAgentToken()
	if a == b {
		t.Fatal("tokens should differ")
	}
}

func TestBuildUpgradeCommand(t *testing.T) {
	cmd := buildUpgradeCommand(installCommandOpts{AgentVersion: "latest"})
	if !strings.Contains(cmd, "LADDER_ACTION=upgrade") {
		t.Fatalf("missing action: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_VERSION=") {
		t.Fatalf("latest should omit version: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_TOKEN=") {
		t.Fatalf("upgrade must not send token: %s", cmd)
	}
	cmd2 := buildUpgradeCommand(installCommandOpts{AgentVersion: "v0.3.1"})
	if !strings.Contains(cmd2, "LADDER_VERSION='v0.3.1'") {
		t.Fatalf("want pinned version: %s", cmd2)
	}
}

func TestBuildUninstallCommand(t *testing.T) {
	cmd := buildUninstallCommand(installCommandOpts{}, false)
	if !strings.Contains(cmd, "LADDER_ACTION=uninstall") {
		t.Fatalf("%s", cmd)
	}
	if strings.Contains(cmd, "LADDER_PURGE=") {
		t.Fatalf("default uninstall should not purge: %s", cmd)
	}
	cmdPurge := buildUninstallCommand(installCommandOpts{}, true)
	if !strings.Contains(cmdPurge, "LADDER_PURGE=1") {
		t.Fatalf("%s", cmdPurge)
	}
}
