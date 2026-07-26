package api

import (
	"strings"
	"testing"
)

func TestBuildInstallCommandRequiresPanelPKI(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		EnrollmentToken: "tok'en",
		AgentVersion:    "latest",
		PanelBaseURL:    "https://panel.example.com/",
		NodeID:          "node-1",
	})
	if !strings.Contains(cmd, `LADDER_ENROLL_TOKEN='tok'\''en'`) {
		t.Fatalf("missing escaped enrollment token: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_VERSION=") {
		t.Fatalf("latest should omit version: %s", cmd)
	}
	if !strings.Contains(cmd, defaultInstallScriptURL) {
		t.Fatalf("missing script url: %s", cmd)
	}
	if !strings.Contains(cmd, "LADDER_PANEL='https://panel.example.com'") ||
		!strings.Contains(cmd, "LADDER_NODE_ID='node-1'") {
		t.Fatalf("missing Panel identity: %s", cmd)
	}
	if strings.Contains(cmd, "LADDER_TLS=") || strings.Contains(cmd, "LADDER_TOKEN=") {
		t.Fatalf("legacy install variables must not be emitted: %s", cmd)
	}
}

func TestBuildInstallCommandWithEnroll(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		EnrollmentToken: "abc",
		PanelBaseURL:    "https://panel.example.com/",
		NodeID:          "nid-1",
		GRPCPort:        50051,
		ReportAddress:   "edge.example.com",
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
	if !strings.Contains(cmd, "LADDER_REPORT_ADDRESS='edge.example.com'") {
		t.Fatalf("report address: %s", cmd)
	}
}

func TestBuildPKIMigrationCommand(t *testing.T) {
	cmd := buildPKIMigrationCommand(installCommandOpts{
		EnrollmentToken: "one-time",
		AgentVersion:    "v0.9.0",
		PanelBaseURL:    "https://panel.example.com/",
		NodeID:          "node-1",
		GRPCPort:        50051,
		ReportAddress:   "192.0.2.10",
	})
	for _, want := range []string{
		"https://github.com/Jlan45/LadderAirport/releases/download/v0.9.0/migrate-agent-to-panel-pki.sh",
		"LADDER_PANEL='https://panel.example.com'",
		"LADDER_NODE_ID='node-1'",
		"LADDER_ENROLL_TOKEN='one-time'",
		"LADDER_VERSION='v0.9.0'",
		"LADDER_GRPC_PORT=50051",
		"LADDER_REPORT_ADDRESS='192.0.2.10'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q: %s", want, cmd)
		}
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
