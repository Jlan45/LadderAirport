//go:build linux

package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ladderairport/agent/internal/fileutil"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// nodeMetricsSampleInterval 是 CPU/网卡计数器两次采样的间隔。
const nodeMetricsSampleInterval = 150 * time.Millisecond

const (
	// bbrRequestFileName 由 root helper（ladder-agent-bbr.path 单元）监听并应用。
	bbrRequestFileName = "bbr.request"
	bbrApplyPollEvery  = 100 * time.Millisecond
	bbrApplyTimeout    = 5 * time.Second
)

// nodeSysCapabilities 上报 Linux 下已实现的节点级能力。
func nodeSysCapabilities() []string {
	return []string{"node-metrics-v1", "bbr-v1"}
}

func (s *Server) GetNodeMetrics(context.Context, *agentv1.GetNodeMetricsRequest) (*agentv1.GetNodeMetricsResponse, error) {
	metrics, err := collectNodeMetrics(s.dataDir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "采集节点系统指标失败：%v", err)
	}
	return metrics, nil
}

func (s *Server) GetBBRStatus(context.Context, *agentv1.GetBBRStatusRequest) (*agentv1.GetBBRStatusResponse, error) {
	current, err := readBBRStatus()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "读取 BBR 状态失败：%v", err)
	}
	return current, nil
}

func (s *Server) SetBBR(ctx context.Context, req *agentv1.SetBBRRequest) (*agentv1.SetBBRResponse, error) {
	return s.applyBBR(ctx, req.GetEnabled(), readBBRStatus)
}

// applyBBR 把 enable/disable 写入 bbr.request，由 root helper 应用 sysctl，
// 随后轮询内核状态直到生效或超时。readStatus 可注入以便测试。
func (s *Server) applyBBR(ctx context.Context, enabled bool, readStatus func() (*agentv1.GetBBRStatusResponse, error)) (*agentv1.SetBBRResponse, error) {
	if current, err := readStatus(); err == nil && bbrTargetReached(current, enabled) {
		return &agentv1.SetBBRResponse{Ok: true, Message: bbrStateMessage(enabled)}, nil
	}
	content := "disable"
	if enabled {
		content = "enable"
	}
	requestPath := filepath.Join(s.dataDir, bbrRequestFileName)
	if err := fileutil.AtomicWrite(requestPath, []byte(content+"\n"), 0o644); err != nil {
		return nil, status.Errorf(codes.Internal, "写入 BBR 请求文件失败：%v", err)
	}
	poll := time.NewTicker(bbrApplyPollEvery)
	defer poll.Stop()
	timeout := time.NewTimer(bbrApplyTimeout)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, status.Error(codes.Canceled, "请求已取消")
		case <-timeout.C:
			return &agentv1.SetBBRResponse{
				Ok:      false,
				Message: "已提交，等待系统应用（如持续失败请确认 ladder-agent-bbr.path 单元已安装）",
			}, nil
		case <-poll.C:
			if current, err := readStatus(); err == nil && bbrTargetReached(current, enabled) {
				return &agentv1.SetBBRResponse{Ok: true, Message: bbrStateMessage(enabled)}, nil
			}
		}
	}
}

// bbrTargetReached 判断当前状态是否已满足目标：开启要求 bbr+fq，关闭只需不再是 bbr。
func bbrTargetReached(current *agentv1.GetBBRStatusResponse, enabled bool) bool {
	if enabled {
		return current.GetCurrentCongestionControl() == "bbr" && current.GetCurrentQdisc() == "fq"
	}
	return current.GetCurrentCongestionControl() != "bbr"
}

func bbrStateMessage(enabled bool) string {
	if enabled {
		return "BBR 已开启"
	}
	return "BBR 已关闭"
}

// collectNodeMetrics 采集整机 CPU/内存/磁盘/网速指标；CPU 与网速基于
// nodeMetricsSampleInterval 间隔的两次计数器采样。
func collectNodeMetrics(dataDir string) (*agentv1.GetNodeMetricsResponse, error) {
	if dataDir == "" {
		dataDir = "."
	}
	prevIdle, prevTotal, err := readProcStatCPU()
	if err != nil {
		return nil, err
	}
	prevRx, prevTx, err := readNetDevBytes()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	time.Sleep(nodeMetricsSampleInterval)
	idle, total, err := readProcStatCPU()
	if err != nil {
		return nil, err
	}
	rx, tx, err := readNetDevBytes()
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start).Seconds()

	memTotal, memAvailable, err := readMeminfo()
	if err != nil {
		return nil, err
	}
	memUsed := uint64(0)
	if memTotal > memAvailable {
		memUsed = memTotal - memAvailable
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &fs); err != nil {
		return nil, fmt.Errorf("读取数据目录磁盘状态失败：%w", err)
	}
	blockSize := uint64(fs.Bsize)

	return &agentv1.GetNodeMetricsResponse{
		CpuPercent:       cpuBusyPercent(prevIdle, prevTotal, idle, total),
		MemoryTotalBytes: memTotal,
		MemoryUsedBytes:  memUsed,
		DiskTotalBytes:   fs.Blocks * blockSize,
		DiskUsedBytes:    (fs.Blocks - fs.Bfree) * blockSize,
		UplinkBps:        counterRate(prevTx, tx, elapsed),
		DownlinkBps:      counterRate(prevRx, rx, elapsed),
		CollectedAtUnix:  time.Now().Unix(),
	}, nil
}

// cpuBusyPercent 由两次 /proc/stat 计数算繁忙百分比（0-100）。
func cpuBusyPercent(prevIdle, prevTotal, idle, total uint64) float64 {
	if total <= prevTotal || idle < prevIdle {
		return 0
	}
	totalDelta := total - prevTotal
	idleDelta := idle - prevIdle
	if idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta)
}

// counterRate 由两次字节计数算 bits/s；计数器回绕或重置时返回 0。
func counterRate(prev, cur uint64, elapsedSeconds float64) uint64 {
	if cur <= prev || elapsedSeconds <= 0 {
		return 0
	}
	return uint64(float64(cur-prev) * 8 / elapsedSeconds)
}

// readProcStatCPU 读取 /proc/stat 的 cpu 汇总行。
func readProcStatCPU() (idle, total uint64, err error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, fmt.Errorf("读取 /proc/stat 失败：%w", err)
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return parseProcStatCPU(line)
}

// parseProcStatCPU 解析 /proc/stat 第一行 cpu 汇总，返回 idle（含 iowait）
// 与总 jiffies；guest/guest_nice 已计入 user/nice，不重复累加。
func parseProcStatCPU(line string) (idle, total uint64, err error) {
	fields := strings.Fields(line)
	if len(fields) < 9 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("解析 /proc/stat cpu 行失败：%q", line)
	}
	for i := 1; i <= 8; i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析 /proc/stat cpu 行失败：%q", line)
		}
		total += v
		if i == 4 || i == 5 { // idle + iowait
			idle += v
		}
	}
	return idle, total, nil
}

// readMeminfo 读取 /proc/meminfo 的 MemTotal/MemAvailable（字节）。
func readMeminfo() (total, available uint64, err error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("读取 /proc/meminfo 失败：%w", err)
	}
	return parseMeminfo(string(b))
}

// parseMeminfo 解析 /proc/meminfo，MemTotal/MemAvailable 单位为 kB，返回字节。
func parseMeminfo(content string) (total, available uint64, err error) {
	for _, line := range strings.Split(content, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		var target *uint64
		switch name {
		case "MemTotal":
			target = &total
		case "MemAvailable":
			target = &available
		default:
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0, 0, fmt.Errorf("解析 /proc/meminfo 失败：%q", line)
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析 /proc/meminfo 失败：%q", line)
		}
		*target = kb * 1024
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("/proc/meminfo 缺少 MemTotal")
	}
	return total, available, nil
}

// readNetDevBytes 读取 /proc/net/dev 并汇总非 lo 接口字节数。
func readNetDevBytes() (rxBytes, txBytes uint64, err error) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, fmt.Errorf("读取 /proc/net/dev 失败：%w", err)
	}
	return parseNetDev(string(b))
}

// parseNetDev 解析 /proc/net/dev：跳过两行表头，逐行取接口名与接收/发送
// 字节数（冒号后第 1、9 列），汇总除 lo 外的全部接口。
func parseNetDev(content string) (rxBytes, txBytes uint64, err error) {
	for i, line := range strings.Split(content, "\n") {
		if i < 2 {
			continue
		}
		name, stats, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(stats)
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("解析 /proc/net/dev 失败：%q", line)
		}
		rxBytes += rx
		txBytes += tx
	}
	return rxBytes, txBytes, nil
}

// readBBRStatus 读取内核 TCP 拥塞控制状态。
func readBBRStatus() (*agentv1.GetBBRStatusResponse, error) {
	current, err := readProcSysTrimmed("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return nil, err
	}
	available, err := readProcSysTrimmed("/proc/sys/net/ipv4/tcp_available_congestion_control")
	if err != nil {
		return nil, err
	}
	qdisc, err := readProcSysTrimmed("/proc/sys/net/core/default_qdisc")
	if err != nil {
		return nil, err
	}
	supported := parseAvailableControls(available)
	return &agentv1.GetBBRStatusResponse{
		BbrAvailable:             slices.Contains(supported, "bbr"),
		BbrEnabled:               current == "bbr" && qdisc == "fq",
		CurrentCongestionControl: current,
		CurrentQdisc:             qdisc,
		SupportedControls:        supported,
	}, nil
}

func readProcSysTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败：%w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// parseAvailableControls 解析 tcp_available_congestion_control 的空格分隔列表。
func parseAvailableControls(content string) []string {
	return strings.Fields(content)
}
