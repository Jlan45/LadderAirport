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
	if strings.Contains(cmd, "LADDER_UPLINK=") {
		t.Fatalf("push install must not set LADDER_UPLINK: %s", cmd)
	}
}

func TestBuildInstallCommandUplink(t *testing.T) {
	cmd := buildInstallCommand(installCommandOpts{
		EnrollmentToken: "abc",
		PanelBaseURL:    "https://panel.example.com/",
		NodeID:          "nid-1",
		Uplink:          true,
	})
	if !strings.Contains(cmd, "LADDER_UPLINK=1") {
		t.Fatalf("missing uplink flag: %s", cmd)
	}
	steps := installSteps("", 0, true)
	joined := strings.Join(steps, "\n")
	if strings.Contains(joined, "mTLS") || !strings.Contains(joined, "不生成管理面私钥") {
		t.Fatalf("uplink steps = %q", joined)
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
