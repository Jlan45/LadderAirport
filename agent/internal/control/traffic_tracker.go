package control

import (
	"context"
	"net"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/bufio"
	satomic "github.com/sagernet/sing/common/atomic"
	N "github.com/sagernet/sing/common/network"
)

// trafficTracker implements adapter.ConnectionTracker and counts
// active connections plus cumulative uplink/downlink bytes for the
// current box instance.
//
// Convention (same as sing-box v2ray stats / clash):
//   - Read from client  → uplink
//   - Write to client   → downlink
type trafficTracker struct {
	uplink   *satomic.Int64
	downlink *satomic.Int64
	active   atomic.Int64
}

func newTrafficTracker() *trafficTracker {
	return &trafficTracker{
		uplink:   new(satomic.Int64),
		downlink: new(satomic.Int64),
	}
}

func (t *trafficTracker) Snapshot() (connections, uplink, downlink int64) {
	return t.active.Load(), t.uplink.Load(), t.downlink.Load()
}

func (t *trafficTracker) RoutedConnection(
	_ context.Context,
	conn net.Conn,
	_ adapter.InboundContext,
	_ adapter.Rule,
	_ adapter.Outbound,
) net.Conn {
	t.active.Add(1)
	counted := bufio.NewInt64CounterConn(conn, []*satomic.Int64{t.uplink}, []*satomic.Int64{t.downlink})
	return &trackedConn{Conn: counted, t: t}
}

func (t *trafficTracker) RoutedPacketConnection(
	_ context.Context,
	conn N.PacketConn,
	_ adapter.InboundContext,
	_ adapter.Rule,
	_ adapter.Outbound,
) N.PacketConn {
	t.active.Add(1)
	counted := bufio.NewInt64CounterPacketConn(conn, []*satomic.Int64{t.uplink}, []*satomic.Int64{t.downlink})
	return &trackedPacketConn{PacketConn: counted, t: t}
}

type trackedConn struct {
	net.Conn
	t      *trafficTracker
	closed atomic.Bool
}

func (c *trackedConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.t.active.Add(-1)
	}
	return c.Conn.Close()
}

type trackedPacketConn struct {
	N.PacketConn
	t      *trafficTracker
	closed atomic.Bool
}

func (c *trackedPacketConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.t.active.Add(-1)
	}
	return c.PacketConn.Close()
}

// UpstreamType implements optional N.ReaderWithUpstream if CounterConn needs unwrapping.
func (c *trackedConn) Upstream() any { return c.Conn }

func (c *trackedPacketConn) Upstream() any { return c.PacketConn }
