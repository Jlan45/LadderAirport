package control

import (
	"fmt"
	"net"

	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

// InterfacesProvider is a pluggable network interface enumerator.
type InterfacesProvider func() ([]*agentv1.NetworkInterface, error)

// listHostInterfaces enumerates host network interfaces for egress selection.
// Returns all interfaces (including loopback/down); the UI decides what to show.
func listHostInterfaces() ([]*agentv1.NetworkInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网卡列表失败：%w", err)
	}
	out := make([]*agentv1.NetworkInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		item := &agentv1.NetworkInterface{
			Name:         iface.Name,
			Up:           iface.Flags&net.FlagUp != 0,
			Loopback:     iface.Flags&net.FlagLoopback != 0,
			Mtu:          int32(iface.MTU),
			HardwareAddr: iface.HardwareAddr.String(),
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, a := range addrs {
				if a == nil {
					continue
				}
				s := a.String()
				if s != "" {
					item.Addresses = append(item.Addresses, s)
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}
