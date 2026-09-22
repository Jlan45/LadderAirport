// Package uplinkhub maintains the Panel side of the Agent↔Panel WebSocket
// uplink. Each connected uplink Agent holds one persistent socket to the
// Panel; the Panel issues AgentControl RPCs over it (probe, logs, protocol
// certificates, FRPS, …) and the Agent replies or streams results back,
// achieving parity with the push/gRPC control plane. Unsolicited status
// reports also arrive on the same socket.
package uplinkhub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/ladderairport/pkg/uplinkws"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// writeTimeout bounds a single frame write so a stalled peer cannot wedge a
// caller. It is independent of the caller context so a caller cancellation
// turns into a cancel frame rather than a corrupting partial write.
const writeTimeout = 15 * time.Second

// maxFrameBytes caps a single inbound frame. ApplyConfig carries the full
// sing-box config JSON, so the limit is generous.
const maxFrameBytes = 16 << 20

// Conn is one live uplink socket from a specific node. It multiplexes many
// concurrent Panel→Agent requests over the single connection, correlating
// replies by frame ID.
type Conn struct {
	ws     *websocket.Conn
	nodeID string

	writeMu sync.Mutex
	nextID  atomic.Uint64

	mu      sync.Mutex
	pending map[uint64]*pendingCall

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error

	onReport func(nodeID string, payload []byte)
}

// pendingCall receives frames for one in-flight request. Unary calls consume a
// single resp frame; streams consume a sequence of stream frames until EOF or
// error.
type pendingCall struct {
	ch     chan *uplinkws.Frame
	stream bool
}

func newConn(nodeID string, ws *websocket.Conn, onReport func(string, []byte)) *Conn {
	ws.SetReadLimit(maxFrameBytes)
	return &Conn{
		ws:       ws,
		nodeID:   nodeID,
		pending:  make(map[uint64]*pendingCall),
		closed:   make(chan struct{}),
		onReport: onReport,
	}
}

// NodeID reports which node this connection belongs to.
func (c *Conn) NodeID() string { return c.nodeID }

// readLoop consumes inbound frames until the socket closes. It is the single
// reader for the connection; all other goroutines only write.
func (c *Conn) readLoop() {
	var err error
	defer func() { c.shutdown(err) }()
	for {
		typ, data, readErr := c.ws.Read(context.Background())
		if readErr != nil {
			err = readErr
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var frame uplinkws.Frame
		if unmarshalErr := json.Unmarshal(data, &frame); unmarshalErr != nil {
			// A malformed frame should not tear down the whole socket.
			continue
		}
		c.dispatch(&frame)
	}
}

func (c *Conn) dispatch(frame *uplinkws.Frame) {
	switch frame.Type {
	case uplinkws.FrameReport:
		if c.onReport != nil {
			c.onReport(c.nodeID, frame.Payload)
		}
	case uplinkws.FrameResponse:
		c.deliverResponse(frame)
	case uplinkws.FrameStream:
		c.deliverStream(frame)
	default:
		// Unknown/unsupported frame types are ignored.
	}
}

func (c *Conn) deliverResponse(frame *uplinkws.Frame) {
	c.mu.Lock()
	call := c.pending[frame.ID]
	if call != nil {
		delete(c.pending, frame.ID)
	}
	c.mu.Unlock()
	if call == nil {
		return
	}
	select {
	case call.ch <- frame:
	default:
	}
}

func (c *Conn) deliverStream(frame *uplinkws.Frame) {
	c.mu.Lock()
	call := c.pending[frame.ID]
	if call != nil && (frame.EOF || frame.Error != nil) {
		delete(c.pending, frame.ID)
	}
	c.mu.Unlock()
	if call == nil {
		return
	}
	select {
	case call.ch <- frame:
	case <-c.closed:
	}
}

func (c *Conn) register(id uint64, stream bool) *pendingCall {
	depth := 1
	if stream {
		depth = 64
	}
	call := &pendingCall{ch: make(chan *uplinkws.Frame, depth), stream: stream}
	c.mu.Lock()
	c.pending[id] = call
	c.mu.Unlock()
	return call
}

func (c *Conn) unregister(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) writeFrame(frame *uplinkws.Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

// call issues a unary Panel→Agent request and waits for the terminal reply.
func (c *Conn) call(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, c.errClosed()
	default:
	}
	id := c.nextID.Add(1)
	call := c.register(id, false)
	defer c.unregister(id)

	if err := c.writeFrame(&uplinkws.Frame{
		Type:    uplinkws.FrameRequest,
		ID:      id,
		Method:  method,
		Payload: payload,
	}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.sendCancel(id)
		return nil, ctx.Err()
	case <-c.closed:
		return nil, c.errClosed()
	case frame := <-call.ch:
		if frame.Error != nil {
			return nil, statusFromError(frame.Error)
		}
		return frame.Payload, nil
	}
}

// openStream issues a streaming Panel→Agent request. The returned streamReader
// yields items until EOF, error, cancellation, or connection loss.
func (c *Conn) openStream(ctx context.Context, method string, payload json.RawMessage) (*streamReader, error) {
	select {
	case <-c.closed:
		return nil, c.errClosed()
	default:
	}
	id := c.nextID.Add(1)
	call := c.register(id, true)

	if err := c.writeFrame(&uplinkws.Frame{
		Type:    uplinkws.FrameRequest,
		ID:      id,
		Method:  method,
		Payload: payload,
	}); err != nil {
		c.unregister(id)
		return nil, err
	}
	return &streamReader{conn: c, id: id, ctx: ctx, call: call}, nil
}

func (c *Conn) sendCancel(id uint64) {
	_ = c.writeFrame(&uplinkws.Frame{Type: uplinkws.FrameCancel, ID: id})
}

func (c *Conn) errClosed() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return errors.New("uplink 连接已关闭")
}

// shutdown marks the connection closed, failing every in-flight call.
func (c *Conn) shutdown(err error) {
	c.closeOnce.Do(func() {
		if err == nil {
			err = errors.New("uplink 连接已关闭")
		}
		c.closeErr = err
		close(c.closed)
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[uint64]*pendingCall)
		c.mu.Unlock()
		for id, call := range pending {
			_ = id
			select {
			case call.ch <- &uplinkws.Frame{Type: uplinkws.FrameResponse, Error: &uplinkws.Error{
				Code:    uint32(codes.Unavailable),
				Message: err.Error(),
			}}:
			default:
			}
		}
		_ = c.ws.Close(websocket.StatusNormalClosure, "closing")
	})
}

// Close tears down the socket and fails pending calls.
func (c *Conn) Close() {
	c.shutdown(errors.New("uplink 连接被主动关闭"))
}

// streamReader consumes one Agent→Panel server stream.
type streamReader struct {
	conn *Conn
	id   uint64
	ctx  context.Context
	call *pendingCall
	done bool
}

func (r *streamReader) recv() (json.RawMessage, error) {
	if r.done {
		return nil, io.EOF
	}
	select {
	case <-r.ctx.Done():
		r.close()
		return nil, r.ctx.Err()
	case <-r.conn.closed:
		r.done = true
		return nil, r.conn.errClosed()
	case frame := <-r.call.ch:
		if frame.Error != nil {
			r.done = true
			return nil, statusFromError(frame.Error)
		}
		if frame.EOF {
			r.done = true
			return nil, io.EOF
		}
		return frame.Payload, nil
	}
}

func (r *streamReader) close() {
	if r.done {
		return
	}
	r.done = true
	r.conn.unregister(r.id)
	r.conn.sendCancel(r.id)
}

// statusFromError converts a wire error back into a gRPC status so capability
// gating (codes.Unimplemented) and other semantics survive the round trip.
func statusFromError(e *uplinkws.Error) error {
	if e == nil {
		return nil
	}
	return status.Error(codes.Code(e.Code), e.Message)
}
