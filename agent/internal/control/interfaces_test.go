package control

import (
	"context"
	"testing"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

func TestListHostInterfaces(t *testing.T) {
	ifaces, err := listHostInterfaces()
	if err != nil {
		t.Fatalf("listHostInterfaces: %v", err)
	}
	// Most platforms expose at least one interface (often loopback).
	// Empty is allowed but should not panic.
	for _, iface := range ifaces {
		if iface == nil {
			t.Fatal("nil interface entry")
		}
		if iface.Name == "" {
			t.Fatal("empty interface name")
		}
	}
}

func TestServerListInterfaces(t *testing.T) {
	s := NewServer(nil, "test", "test", nil)
	resp, err := s.ListInterfaces(context.Background(), &agentv1.ListInterfacesRequest{})
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	for _, iface := range resp.GetInterfaces() {
		if iface.GetName() == "" {
			t.Fatal("empty name in response")
		}
	}
}

func TestServerListInterfacesProvider(t *testing.T) {
	s := NewServer(nil, "test", "test", nil)
	s.SetInterfacesProvider(func() ([]*agentv1.NetworkInterface, error) {
		return []*agentv1.NetworkInterface{{Name: "fake0", Up: true}}, nil
	})
	resp, err := s.ListInterfaces(context.Background(), &agentv1.ListInterfacesRequest{})
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(resp.GetInterfaces()) != 1 || resp.GetInterfaces()[0].GetName() != "fake0" {
		t.Fatalf("unexpected interfaces: %+v", resp.GetInterfaces())
	}
}

func TestPingAddsNodeMetricsWhenProviderSet(t *testing.T) {
	s := NewServer(nil, "test", "test", nil)
	s.SetNodeMetricsProvider(func() (*agentv1.GetNodeMetricsResponse, error) {
		return &agentv1.GetNodeMetricsResponse{}, nil
	})
	ping, err := s.Ping(context.Background(), &agentv1.PingRequest{})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if !containsString(ping.GetCapabilities(), "node-metrics-v1") {
		t.Fatalf("expected node-metrics-v1 when provider set, got %v", ping.GetCapabilities())
	}
}
