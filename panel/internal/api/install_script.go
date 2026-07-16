package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// Default raw install script URL (main branch). Override via request field if needed.
const defaultInstallScriptURL = "https://raw.githubusercontent.com/Jlan45/LadderAirport/main/scripts/install-agent.sh"

func randomAgentToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// installCommandOpts configures the curl|bash one-liner.
type installCommandOpts struct {
	ScriptURL    string
	Token        string
	AgentVersion string
	EnableTLS    bool
	PanelBaseURL string // e.g. https://panel.example.com — enables auto-enroll
	NodeID       string
	GRPCPort     int
	Listen       string // optional LADDER_LISTEN override
}

// buildInstallCommand produces a one-liner for curl|bash install with token, TLS, and optional enroll.
func buildInstallCommand(opts installCommandOpts) string {
	scriptURL := opts.ScriptURL
	if scriptURL == "" {
		scriptURL = defaultInstallScriptURL
	}
	tlsVal := "1"
	if !opts.EnableTLS {
		tlsVal = "0"
	}
	var b strings.Builder
	b.WriteString("curl -fsSL ")
	b.WriteString(shellSingleQuote(scriptURL))
	b.WriteString(" | sudo env")
	b.WriteString(" LADDER_TOKEN=")
	b.WriteString(shellSingleQuote(opts.Token))
	b.WriteString(" LADDER_TLS=")
	b.WriteString(tlsVal)
	if opts.AgentVersion != "" && opts.AgentVersion != "latest" {
		b.WriteString(" LADDER_VERSION=")
		b.WriteString(shellSingleQuote(opts.AgentVersion))
	}
	panel := strings.TrimRight(strings.TrimSpace(opts.PanelBaseURL), "/")
	if panel != "" {
		b.WriteString(" LADDER_PANEL=")
		b.WriteString(shellSingleQuote(panel))
		if opts.NodeID != "" {
			b.WriteString(" LADDER_NODE_ID=")
			b.WriteString(shellSingleQuote(opts.NodeID))
		}
		if opts.GRPCPort > 0 {
			b.WriteString(" LADDER_GRPC_PORT=")
			b.WriteString(fmt.Sprintf("%d", opts.GRPCPort))
		}
	}
	if opts.Listen != "" {
		b.WriteString(" LADDER_LISTEN=")
		b.WriteString(shellSingleQuote(opts.Listen))
	}
	b.WriteString(" bash")
	return b.String()
}

// buildUpgradeCommand produces a curl|bash one-liner that only replaces the binary
// (LADDER_ACTION=upgrade). Token/TLS/env are left untouched on the node.
func buildUpgradeCommand(opts installCommandOpts) string {
	scriptURL := opts.ScriptURL
	if scriptURL == "" {
		scriptURL = defaultInstallScriptURL
	}
	var b strings.Builder
	b.WriteString("curl -fsSL ")
	b.WriteString(shellSingleQuote(scriptURL))
	b.WriteString(" | sudo env LADDER_ACTION=upgrade")
	if opts.AgentVersion != "" && opts.AgentVersion != "latest" {
		b.WriteString(" LADDER_VERSION=")
		b.WriteString(shellSingleQuote(opts.AgentVersion))
	}
	b.WriteString(" bash")
	return b.String()
}

// buildUninstallCommand produces a curl|bash one-liner to stop the agent service
// and remove unit/binary. Purge removes conf/data as well.
func buildUninstallCommand(opts installCommandOpts, purge bool) string {
	scriptURL := opts.ScriptURL
	if scriptURL == "" {
		scriptURL = defaultInstallScriptURL
	}
	var b strings.Builder
	b.WriteString("curl -fsSL ")
	b.WriteString(shellSingleQuote(scriptURL))
	b.WriteString(" | sudo env LADDER_ACTION=uninstall")
	if purge {
		b.WriteString(" LADDER_PURGE=1")
	}
	b.WriteString(" bash")
	return b.String()
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func panelBaseFromSettings(publicBaseURL string) string {
	return strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
}

func installSteps(enableTLS bool, address string, grpcPort int, panelBase string, enrollOK bool) []string {
	steps := []string{
		"在目标服务器（Linux amd64/arm64）以 root 执行上方一键安装命令。",
		"安装脚本会下载 ladder-agent、写入 systemd，并生成 TLS 证书（LADDER_TLS=0 可关）。",
	}
	if enrollOK && panelBase != "" {
		steps = append(steps,
			"安装结束会自动向 Panel 上报地址与 CA（POST /api/v1/agent/enroll），无需手填。",
			"回到 Panel 刷新节点列表，确认地址/CA 已写入后点「探测」。",
		)
		if strings.TrimSpace(address) != "" {
			steps = append(steps, fmt.Sprintf("若上报 IP 不对，可在节点详情改地址（期望 gRPC %d）。", grpcPort))
		}
	} else {
		steps = append(steps,
			"未配置 Public Base URL：无法自动上报。请在「设置」填写 Panel 公网地址（如 https://panel.example.com）后重新生成安装命令。",
		)
		if enableTLS {
			steps = append(steps,
				"或手动：sudo cat /etc/ladder-agent/tls/ca.crt 粘贴到节点 CA，并填写地址。",
			)
		}
	}
	steps = append(steps, "探测成功后即可关联入站并下发配置。")
	return steps
}
