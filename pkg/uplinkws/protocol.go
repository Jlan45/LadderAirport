// Package uplinkws defines the JSON wire protocol for the Agent↔Panel
// WebSocket uplink. The uplink is a single persistent, bidirectional channel
// that carries the full AgentControl surface: Panel issues RPC requests to the
// Agent (probe, logs, protocol certificates, FRPS, …) and the Agent replies or
// streams results back, achieving parity with the push/gRPC control plane.
//
// The protocol is proto-agnostic here: each frame carries a method name and an
// opaque JSON payload. Both ends marshal/unmarshal the concrete agent.v1
// messages with protojson so the shapes stay identical to gRPC.
package uplinkws

import "encoding/json"

// Subprotocol is negotiated on the WebSocket handshake so both ends agree on
// the frame schema and can evolve it later.
const Subprotocol = "ladder-uplink-v1"

// FrameType tags the role of a frame on the wire.
type FrameType string

const (
	// FrameRequest is a Panel→Agent RPC invocation (unary or the opening of a
	// server stream). Carries Method + Payload + a correlation ID.
	FrameRequest FrameType = "req"
	// FrameResponse is the Agent→Panel terminal reply to a FrameRequest. Carries
	// the same ID and either Payload or Error.
	FrameResponse FrameType = "resp"
	// FrameStream is one Agent→Panel item of a server stream (e.g. a log line),
	// correlated by ID. EOF marks the end of the stream; Error terminates it.
	FrameStream FrameType = "stream"
	// FrameCancel is a Panel→Agent request to abort an in-flight request/stream.
	FrameCancel FrameType = "cancel"
	// FrameReport is an unsolicited Agent→Panel status push (runtime state,
	// metrics, capabilities). It mirrors the HTTP /agent/report body.
	FrameReport FrameType = "report"
)

// Frame is the single envelope exchanged over the socket.
type Frame struct {
	Type    FrameType       `json:"type"`
	ID      uint64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	// EOF marks the final FrameStream item for an ID (no more items follow).
	EOF bool `json:"eof,omitempty"`
}

// Error carries a gRPC status code and message so the Panel can preserve the
// exact error semantics (e.g. codes.Unimplemented for capability gating).
type Error struct {
	Code    uint32 `json:"code"`
	Message string `json:"message"`
}

// Method names for every AgentControl RPC. Kept as string constants shared by
// both ends so the dispatch tables cannot drift.
const (
	MethodPing                                = "Ping"
	MethodApplyConfig                         = "ApplyConfig"
	MethodStart                               = "Start"
	MethodStop                                = "Stop"
	MethodGetStatus                           = "GetStatus"
	MethodGetMetrics                          = "GetMetrics"
	MethodStreamLogs                          = "StreamLogs"
	MethodListInterfaces                      = "ListInterfaces"
	MethodUpgradeAgent                        = "UpgradeAgent"
	MethodProbeOutbound                       = "ProbeOutbound"
	MethodGetPublicAddresses                  = "GetPublicAddresses"
	MethodPrepareProtocolCertificate          = "PrepareProtocolCertificate"
	MethodInstallProtocolCertificate          = "InstallProtocolCertificate"
	MethodGetProtocolCertificateStatus        = "GetProtocolCertificateStatus"
	MethodDeleteProtocolCertificateGeneration = "DeleteProtocolCertificateGeneration"
	MethodApplyFRPServerConfig                = "ApplyFRPServerConfig"
	MethodStartFRPServer                      = "StartFRPServer"
	MethodStopFRPServer                       = "StopFRPServer"
	MethodGetFRPServerStatus                  = "GetFRPServerStatus"
	MethodGetFRPServerMappings                = "GetFRPServerMappings"
	MethodGetNodeMetrics                      = "GetNodeMetrics"
	MethodGetBBRStatus                        = "GetBBRStatus"
	MethodSetBBR                              = "SetBBR"
)
