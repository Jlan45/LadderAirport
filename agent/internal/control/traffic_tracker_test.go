package control

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

func TestTrafficTrackerCountsBytesAndConns(t *testing.T) {
	tr := newTrafficTracker()
	c1, c2 := net.Pipe()
	defer c2.Close()

	wrapped := tr.RoutedConnection(context.Background(), c1, adapter.InboundContext{}, nil, nil)
	conns, up, down := tr.Snapshot()
	if conns != 1 {
		t.Fatalf("connections = %d, want 1", conns)
	}
	if up != 0 || down != 0 {
		t.Fatalf("expected zero traffic, got up=%d down=%d", up, down)
	}

	// Peer writes → Read on tracked conn counts as uplink.
	errCh := make(chan error, 1)
	go func() {
		_, err := c2.Write([]byte("hello-uplink"))
		errCh <- err
	}()
	buf := make([]byte, 32)
	n, err := wrapped.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Fatalf("read n=%d", n)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	// Write to peer → downlink.
	go func() {
		_, _ = io.ReadFull(c2, make([]byte, 2))
	}()
	if _, err := wrapped.Write([]byte("dl")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	conns, up, down = tr.Snapshot()
	if conns != 1 {
		t.Fatalf("connections = %d", conns)
	}
	if up < 12 {
		t.Fatalf("uplink = %d, want >= 12", up)
	}
	if down < 2 {
		t.Fatalf("downlink = %d, want >= 2", down)
	}

	if err := wrapped.Close(); err != nil {
		t.Fatal(err)
	}
	// double close should not underflow
	_ = wrapped.Close()
	conns, _, _ = tr.Snapshot()
	if conns != 0 {
		t.Fatalf("after close connections = %d", conns)
	}
}
