package control

import (
	"context"
	"strings"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements agentv1.AgentControlServer.
type Server struct {
	agentv1.UnimplementedAgentControlServer

	rt             Runtime
	agentVersion   string
	singboxVersion string
	logs           *LogBuf
}

// NewServer constructs an AgentControl server.
// If logs is nil, a default ring buffer is created.
func NewServer(rt Runtime, agentVersion, singboxVersion string, logs *LogBuf) *Server {
	if logs == nil {
		logs = NewLogBuf(defaultLogBufSize)
	}
	return &Server{
		rt:             rt,
		agentVersion:   agentVersion,
		singboxVersion: singboxVersion,
		logs:           logs,
	}
}

func (s *Server) Ping(context.Context, *agentv1.PingRequest) (*agentv1.PingResponse, error) {
	return &agentv1.PingResponse{
		AgentVersion:   s.agentVersion,
		SingboxVersion: s.singboxVersion,
		Capabilities:   []string{"proxy_chain_v1"},
	}, nil
}

func (s *Server) ProbeOutbound(ctx context.Context, req *agentv1.ProbeOutboundRequest) (*agentv1.ProbeOutboundResponse, error) {
	if req == nil || strings.TrimSpace(req.GetOutboundTag()) == "" {
		return nil, status.Error(codes.InvalidArgument, "必须提供 outbound_tag")
	}
	targetURL := strings.TrimSpace(req.GetUrl())
	if targetURL == "" {
		targetURL = "https://www.gstatic.com/generate_204"
	}
	delay, err := s.rt.ProbeOutbound(ctx, req.GetOutboundTag(), targetURL)
	if err != nil {
		return &agentv1.ProbeOutboundResponse{Ok: false, Message: err.Error()}, nil
	}
	return &agentv1.ProbeOutboundResponse{
		Ok:      true,
		DelayMs: delay,
		Message: "正常",
	}, nil
}

func (s *Server) ApplyConfig(ctx context.Context, req *agentv1.ApplyConfigRequest) (*agentv1.ApplyConfigResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "请求不能为空")
	}
	if err := s.rt.Apply(ctx, req.GetConfigJson(), req.GetConfigHash()); err != nil {
		return &agentv1.ApplyConfigResponse{
			Ok:      false,
			Message: err.Error(),
		}, nil
	}
	return &agentv1.ApplyConfigResponse{
		Ok:          true,
		Message:     "配置已下发",
		AppliedHash: req.GetConfigHash(),
	}, nil
}

func (s *Server) Start(ctx context.Context, _ *agentv1.StartRequest) (*agentv1.StartResponse, error) {
	if err := s.rt.Start(ctx); err != nil {
		return &agentv1.StartResponse{Ok: false, Message: err.Error()}, nil
	}
	return &agentv1.StartResponse{Ok: true, Message: "已启动"}, nil
}

func (s *Server) Stop(ctx context.Context, _ *agentv1.StopRequest) (*agentv1.StopResponse, error) {
	if err := s.rt.Stop(ctx); err != nil {
		return &agentv1.StopResponse{Ok: false, Message: err.Error()}, nil
	}
	return &agentv1.StopResponse{Ok: true, Message: "已停止"}, nil
}

func (s *Server) GetStatus(ctx context.Context, _ *agentv1.GetStatusRequest) (*agentv1.GetStatusResponse, error) {
	st := s.rt.Status(ctx)
	return &agentv1.GetStatusResponse{
		State:         string(st.State),
		ConfigHash:    st.ConfigHash,
		StartedAtUnix: st.StartedAtUnix,
		LastError:     st.LastError,
	}, nil
}

func (s *Server) GetMetrics(ctx context.Context, _ *agentv1.GetMetricsRequest) (*agentv1.GetMetricsResponse, error) {
	m := s.rt.Metrics(ctx)
	return &agentv1.GetMetricsResponse{
		Connections:    m.Connections,
		UplinkBytes:    m.UplinkBytes,
		DownlinkBytes:  m.DownlinkBytes,
		CpuPercent:     m.CPUPercent,
		MemoryRssBytes: m.MemoryRSSBytes,
	}, nil
}

func (s *Server) ListInterfaces(context.Context, *agentv1.ListInterfacesRequest) (*agentv1.ListInterfacesResponse, error) {
	ifaces, err := listHostInterfaces()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "获取网卡列表失败：%v", err)
	}
	return &agentv1.ListInterfacesResponse{Interfaces: ifaces}, nil
}

func (s *Server) UpgradeAgent(ctx context.Context, req *agentv1.UpgradeAgentRequest) (*agentv1.UpgradeAgentResponse, error) {
	if req == nil {
		req = &agentv1.UpgradeAgentRequest{}
	}
	res, err := StageAgentUpgrade(ctx, UpgradeRequest{
		Version:     req.GetVersion(),
		Repo:        req.GetRepo(),
		DownloadURL: req.GetDownloadUrl(),
		SHA256:      req.GetSha256(),
	})
	if err != nil {
		return &agentv1.UpgradeAgentResponse{
			Ok:              false,
			Message:         err.Error(),
			PreviousVersion: s.agentVersion,
		}, nil
	}
	return &agentv1.UpgradeAgentResponse{
		Ok:              true,
		Message:         res.Message,
		Version:         res.Version,
		StagedPath:      res.StagedPath,
		PreviousVersion: s.agentVersion,
	}, nil
}

func (s *Server) StreamLogs(req *agentv1.StreamLogsRequest, stream agentv1.AgentControl_StreamLogsServer) error {
	levelFilter := ""
	tailN := 0
	if req != nil {
		levelFilter = strings.ToLower(strings.TrimSpace(req.GetLevel()))
		tailN = int(req.GetTail())
	}

	// Drain historical tail, then stream live lines until context is done.
	for _, line := range s.logs.Tail(tailN) {
		if !levelMatch(levelFilter, line.Level) {
			continue
		}
		if err := stream.Send(toProtoLogLine(line)); err != nil {
			return err
		}
	}

	live, cancel := s.logs.Subscribe()
	defer cancel()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-live:
			if !ok {
				return nil
			}
			if !levelMatch(levelFilter, line.Level) {
				continue
			}
			if err := stream.Send(toProtoLogLine(line)); err != nil {
				return err
			}
		}
	}
}

func levelMatch(filter, level string) bool {
	if filter == "" {
		return true
	}
	return strings.EqualFold(filter, level)
}

func toProtoLogLine(line LogLine) *agentv1.LogLine {
	return &agentv1.LogLine{
		TsUnixMs: line.TsUnixMs,
		Level:    line.Level,
		Message:  line.Message,
	}
}
