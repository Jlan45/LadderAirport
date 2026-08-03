//go:build !linux

package control

import (
	"context"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// nodeSysCapabilities 非 Linux 平台未实现节点系统指标与 BBR 控制，不上报对应能力。
func nodeSysCapabilities() []string {
	return nil
}

func (s *Server) GetNodeMetrics(context.Context, *agentv1.GetNodeMetricsRequest) (*agentv1.GetNodeMetricsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "节点系统指标仅支持 Linux")
}

func (s *Server) GetBBRStatus(context.Context, *agentv1.GetBBRStatusRequest) (*agentv1.GetBBRStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "BBR 状态仅支持 Linux")
}

func (s *Server) SetBBR(context.Context, *agentv1.SetBBRRequest) (*agentv1.SetBBRResponse, error) {
	return nil, status.Error(codes.Unimplemented, "BBR 控制仅支持 Linux")
}
