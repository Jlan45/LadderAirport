//go:build linux

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

func TestParseProcStatCPU(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantIdle  uint64
		wantTotal uint64
		wantErr   bool
	}{
		{
			name:      "十列（排除 guest）",
			line:      "cpu  100 200 300 400 500 600 700 800 900 1000",
			wantIdle:  900,  // idle 400 + iowait 500
			wantTotal: 3600, // user..steal 求和，不含 guest 900 与 guest_nice 1000
		},
		{
			name:      "八列（无 guest 列）",
			line:      "cpu  1 2 3 4 5 6 7 8",
			wantIdle:  9,
			wantTotal: 36,
		},
		{name: "字段不足", line: "cpu 1 2 3", wantErr: true},
		{name: "缺少 cpu 汇总前缀", line: "cpu0 1 2 3 4 5 6 7 8", wantErr: true},
		{name: "非数字字段", line: "cpu a b c d e f g h", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idle, total, err := parseProcStatCPU(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，得到 idle=%d total=%d", idle, total)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if idle != tt.wantIdle || total != tt.wantTotal {
				t.Fatalf("idle=%d total=%d，期望 idle=%d total=%d", idle, total, tt.wantIdle, tt.wantTotal)
			}
		})
	}
}

func TestCPUBusyPercent(t *testing.T) {
	tests := []struct {
		name                string
		prevIdle, prevTotal uint64
		idle, total         uint64
		want                float64
	}{
		{name: "75% 繁忙", prevIdle: 100, prevTotal: 1000, idle: 125, total: 1100, want: 75},
		{name: "全部繁忙", prevIdle: 100, prevTotal: 1000, idle: 100, total: 1100, want: 100},
		{name: "计数器未前进", prevIdle: 100, prevTotal: 1000, idle: 100, total: 1000, want: 0},
		{name: "计数器回退", prevIdle: 100, prevTotal: 1000, idle: 50, total: 500, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuBusyPercent(tt.prevIdle, tt.prevTotal, tt.idle, tt.total); got != tt.want {
				t.Fatalf("cpuBusyPercent = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestParseMeminfo(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantTotal     uint64
		wantAvailable uint64
		wantErr       bool
	}{
		{
			name: "标准内容",
			content: "MemTotal:       16384000 kB\n" +
				"MemFree:         8192000 kB\n" +
				"MemAvailable:   12288000 kB\n" +
				"Buffers:          512000 kB\n",
			wantTotal:     16384000 * 1024,
			wantAvailable: 12288000 * 1024,
		},
		{name: "缺少 MemTotal", content: "MemFree: 1 kB\n", wantErr: true},
		{name: "数值非法", content: "MemTotal: abc kB\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, available, err := parseMeminfo(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，得到 total=%d available=%d", total, available)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if total != tt.wantTotal || available != tt.wantAvailable {
				t.Fatalf("total=%d available=%d，期望 total=%d available=%d",
					total, available, tt.wantTotal, tt.wantAvailable)
			}
		})
	}
}

func TestParseNetDev(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantRx  uint64
		wantTx  uint64
		wantErr bool
	}{
		{
			name: "多接口跳过 lo",
			content: "Inter-|   Receive                                                |  Transmit\n" +
				" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
				"    lo: 1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0\n" +
				"  eth0: 5000      50    0    0    0     0          0         0     3000      30    0    0    0     0       0          0\n" +
				"  eth1: 2000      20    1    0    0     0          0         0     4000      40    0    0    0     0       0          0\n",
			wantRx: 7000,
			wantTx: 7000,
		},
		{
			name:    "只有表头",
			content: "Inter-|   Receive  |  Transmit\n face |bytes      |bytes\n",
			wantRx:  0,
			wantTx:  0,
		},
		{
			name: "字节数非法",
			content: "Inter-|   Receive  |  Transmit\n face |bytes      |bytes\n" +
				"  eth0: abc 50 0 0 0 0 0 0 3000 30 0 0 0 0 0 0\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rx, tx, err := parseNetDev(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，得到 rx=%d tx=%d", rx, tx)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if rx != tt.wantRx || tx != tt.wantTx {
				t.Fatalf("rx=%d tx=%d，期望 rx=%d tx=%d", rx, tx, tt.wantRx, tt.wantTx)
			}
		})
	}
}

func TestParseAvailableControls(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{name: "含 bbr", content: "reno cubic bbr\n", want: []string{"reno", "cubic", "bbr"}},
		{name: "多余空白", content: "  cubic   reno \n", want: []string{"cubic", "reno"}},
		{name: "空内容", content: "\n", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAvailableControls(tt.content); !slices.Equal(got, tt.want) {
				t.Fatalf("parseAvailableControls = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestBBRTargetReached(t *testing.T) {
	tests := []struct {
		name    string
		current *agentv1.GetBBRStatusResponse
		enabled bool
		want    bool
	}{
		{
			name:    "开启且已生效",
			current: &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "bbr", CurrentQdisc: "fq"},
			enabled: true,
			want:    true,
		},
		{
			name:    "bbr 已开但 qdisc 非 fq",
			current: &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "bbr", CurrentQdisc: "fq_codel"},
			enabled: true,
			want:    false,
		},
		{
			name:    "开启但未生效",
			current: &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "cubic", CurrentQdisc: "fq"},
			enabled: true,
			want:    false,
		},
		{
			name:    "关闭且已生效",
			current: &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "cubic", CurrentQdisc: "fq"},
			enabled: false,
			want:    true,
		},
		{
			name:    "关闭但仍是 bbr",
			current: &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "bbr", CurrentQdisc: "fq"},
			enabled: false,
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bbrTargetReached(tt.current, tt.enabled); got != tt.want {
				t.Fatalf("bbrTargetReached = %v，期望 %v", got, tt.want)
			}
		})
	}
}

// 已是目标状态时直接返回 ok=true，不写 bbr.request 文件。
func TestApplyBBRAlreadyAtTarget(t *testing.T) {
	dataDir := t.TempDir()
	s := &Server{dataDir: dataDir}
	readStatus := func() (*agentv1.GetBBRStatusResponse, error) {
		return &agentv1.GetBBRStatusResponse{
			BbrAvailable:             true,
			BbrEnabled:               true,
			CurrentCongestionControl: "bbr",
			CurrentQdisc:             "fq",
			SupportedControls:        []string{"reno", "cubic", "bbr"},
		}, nil
	}
	resp, err := s.applyBBR(context.Background(), true, readStatus)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() {
		t.Fatalf("resp = %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(dataDir, bbrRequestFileName)); !os.IsNotExist(err) {
		t.Fatalf("已是目标状态不应写请求文件，stat err = %v", err)
	}
}

// 写入请求文件后轮询到目标状态即返回 ok=true。
func TestApplyBBRPollUntilApplied(t *testing.T) {
	dataDir := t.TempDir()
	s := &Server{dataDir: dataDir}
	calls := 0
	readStatus := func() (*agentv1.GetBBRStatusResponse, error) {
		calls++
		current := &agentv1.GetBBRStatusResponse{CurrentCongestionControl: "cubic", CurrentQdisc: "pfifo_fast"}
		if calls > 1 { // 首次检查后视为 root helper 已应用
			current.CurrentCongestionControl = "bbr"
			current.CurrentQdisc = "fq"
		}
		return current, nil
	}
	resp, err := s.applyBBR(context.Background(), true, readStatus)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() {
		t.Fatalf("resp = %+v", resp)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, bbrRequestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "enable" {
		t.Fatalf("请求文件内容 = %q，期望 enable", content)
	}
}

// 真实 /proc 冒烟：采集应成功且关键字段非零。
func TestCollectNodeMetricsSmoke(t *testing.T) {
	metrics, err := collectNodeMetrics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if metrics.GetMemoryTotalBytes() == 0 || metrics.GetDiskTotalBytes() == 0 {
		t.Fatalf("metrics = %+v", metrics)
	}
	if metrics.GetMemoryUsedBytes() > metrics.GetMemoryTotalBytes() {
		t.Fatalf("内存已用超过总量：%+v", metrics)
	}
	if metrics.GetCollectedAtUnix() == 0 {
		t.Fatalf("缺少采集时间：%+v", metrics)
	}
}

func TestReadBBRStatusSmoke(t *testing.T) {
	// 容器化 /proc/sys 可能不完整（如 Docker 缺 default_qdisc），真实主机必有。
	if _, err := os.Stat("/proc/sys/net/core/default_qdisc"); errors.Is(err, os.ErrNotExist) {
		t.Skip("/proc/sys/net/core/default_qdisc 不存在，跳过冒烟")
	}
	current, err := readBBRStatus()
	if err != nil {
		t.Fatal(err)
	}
	if current.GetCurrentCongestionControl() == "" || len(current.GetSupportedControls()) == 0 {
		t.Fatalf("status = %+v", current)
	}
}
