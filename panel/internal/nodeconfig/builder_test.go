package nodeconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ladderairport/panel/internal/store"
)

func TestBuildFRPInboundUsesLoopbackHighPort(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	inbound := &store.InboundConfig{
		Name: "ss-frp", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": 8388, "method": "aes-256-gcm", "password": "secret",
		},
	}
	if err := st.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInboundBindings(node.ID, []store.NodeInboundBinding{{
		InboundID: inbound.ID, FRPEnabled: true,
		FRPCConfig: `{"server_addr":"frps.example.com","server_port":7000,"remote_port":20001,"token":"secret"}`,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := (&Builder{Store: st}).Build(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.JSON), &document); err != nil {
		t.Fatal(err)
	}
	entry := document["inbounds"].([]any)[0].(map[string]any)
	if entry["listen"] != "127.0.0.1" {
		t.Fatalf("listen = %v", entry["listen"])
	}
	if entry["disable_listen"] != nil {
		t.Fatalf("unexpected disable_listen = %v", entry["disable_listen"])
	}
	localPort := int(entry["listen_port"].(float64))
	if localPort < 49152 || localPort > 65535 || localPort == 8388 {
		t.Fatalf("local listen_port = %d", localPort)
	}
	tag := entry["tag"].(string)
	frpc := document["ladder_frpc"].(map[string]any)
	var connection map[string]any
	if err := json.Unmarshal([]byte(frpc[tag].(string)), &connection); err != nil {
		t.Fatal(err)
	}
	if int(connection["local_port"].(float64)) != localPort || int(connection["remote_port"].(float64)) != 20001 {
		t.Fatalf("FRPC mapping = %v", connection)
	}
	otherNode := &store.Node{Name: "direct", Address: "192.0.2.11", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(otherNode); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(otherNode.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	other, err := (&Builder{Store: st}).Build(otherNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	document = nil
	if err := json.Unmarshal([]byte(other.JSON), &document); err != nil {
		t.Fatal(err)
	}
	if document["ladder_frpc"] != nil || document["inbounds"].([]any)[0].(map[string]any)["listen"] != "0.0.0.0" {
		t.Fatalf("FRP setting leaked to other node: %s", other.JSON)
	}
}

func TestAllocateFRPLocalPortStableAndSkipsOccupied(t *testing.T) {
	first, err := allocateFRPLocalPort("inbound-1", map[int]bool{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocateFRPLocalPort("inbound-1", map[int]bool{})
	if err != nil || second != first {
		t.Fatalf("port changed from %d to %d: %v", first, second, err)
	}
	available, err := allocateFRPLocalPort("inbound-1", map[int]bool{first: true})
	if err != nil || available == first || available < 49152 || available > 65535 {
		t.Fatalf("occupied port was selected: %d, %v", available, err)
	}
}

func TestBuildOverlaysManagedTLSWithoutMutatingGlobalInbound(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	inbound := &store.InboundConfig{
		Name: "trojan", Protocol: "trojan", Enabled: true,
		Params: map[string]any{
			"listen": "0.0.0.0", "port": 443, "password": "secret",
			"tls_cert_path": "/legacy/cert.pem", "tls_key_path": "/legacy/key.pem",
		},
	}
	if err := st.CreateInbound(inbound); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeInbounds(node.ID, []string{inbound.ID}); err != nil {
		t.Fatal(err)
	}
	dnsAccount := &store.DNSAccount{
		Name: "DNS", Provider: "cloudflare", Zone: "example.com",
		CredentialsCiphertext: "encrypted",
		Settings:              map[string]any{}, Enabled: true,
	}
	if err := st.CreateDNSAccount(dnsAccount); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: dnsAccount.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "manual",
		ManualIPv4: "192.0.2.10", TTL: 300, Enabled: true, State: "ready",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	acmeAccount := &store.ACMEAccount{
		Name: "CA", DirectoryURL: "https://ca.example/directory",
		AccountKeyCiphertext: "encrypted", Status: "active",
	}
	if err := st.CreateACMEAccount(acmeAccount); err != nil {
		t.Fatal(err)
	}
	certificate := &store.ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: acmeAccount.ID,
		Domains: []string{domain.FQDN}, Status: "active",
		ActiveCertPath: "/managed/r1/fullchain.pem",
		ActiveKeyPath:  "/managed/r1/privkey.pem", Revision: 1,
	}
	if err := st.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeInboundTLSBinding(&store.NodeInboundTLSBinding{
		NodeID: node.ID, InboundID: inbound.ID, Mode: "managed",
		ManagedDomainID: domain.ID, CertificateID: certificate.ID,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := (&Builder{Store: st}).Build(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(result.JSON), &config); err != nil {
		t.Fatal(err)
	}
	inbounds := config["inbounds"].([]any)
	tls := inbounds[0].(map[string]any)["tls"].(map[string]any)
	if tls["certificate_path"] != "/managed/r1/fullchain.pem" ||
		tls["key_path"] != "/managed/r1/privkey.pem" ||
		tls["server_name"] != "edge.example.com" {
		t.Fatalf("tls = %+v", tls)
	}
	stored, err := st.GetInbound(inbound.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Params["tls_cert_path"] != "/legacy/cert.pem" ||
		stored.Params["server_name"] != nil {
		t.Fatalf("global inbound was mutated: %+v", stored.Params)
	}
	resolved, hostname, managed, err := (&Builder{Store: st}).ResolveManagedTLS(node.ID, *stored)
	if err != nil {
		t.Fatal(err)
	}
	if !managed || hostname != "edge.example.com" ||
		resolved.Params["tls_cert_path"] != "/managed/r1/fullchain.pem" {
		t.Fatalf("resolved=%+v hostname=%s managed=%v", resolved, hostname, managed)
	}
	endpoint, err := (&Builder{Store: st}).ResolveHopEndpoint(store.ProxyChain{
		Name: "managed-chain",
		Hops: []store.ProxyChainHop{{
			NodeID: node.ID, InboundID: inbound.ID,
		}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Server != "edge.example.com" ||
		endpoint.Params["server_name"] != "edge.example.com" {
		t.Fatalf("managed chain endpoint = %+v", endpoint)
	}
}

func TestResolveManagedTLSIndexedMatchesPerNodeLookup(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "online"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	managed := &store.InboundConfig{
		Name: "trojan", Protocol: "trojan", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 443, "password": "secret"},
	}
	plain := &store.InboundConfig{
		Name: "ss", Protocol: "shadowsocks", Enabled: true,
		Params: map[string]any{"listen": "0.0.0.0", "port": 8388, "method": "aes-128-gcm", "password": "p"},
	}
	for _, in := range []*store.InboundConfig{managed, plain} {
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetNodeInbounds(node.ID, []string{managed.ID, plain.ID}); err != nil {
		t.Fatal(err)
	}
	dnsAccount := &store.DNSAccount{
		Name: "DNS", Provider: "cloudflare", Zone: "example.com",
		CredentialsCiphertext: "encrypted", Settings: map[string]any{}, Enabled: true,
	}
	if err := st.CreateDNSAccount(dnsAccount); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: dnsAccount.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "manual",
		ManualIPv4: "192.0.2.10", TTL: 300, Enabled: true, State: "ready",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	acmeAccount := &store.ACMEAccount{
		Name: "CA", DirectoryURL: "https://ca.example/directory",
		AccountKeyCiphertext: "encrypted", Status: "active",
	}
	if err := st.CreateACMEAccount(acmeAccount); err != nil {
		t.Fatal(err)
	}
	certificate := &store.ProtocolCertificate{
		NodeID: node.ID, ManagedDomainID: domain.ID, ACMEAccountID: acmeAccount.ID,
		Domains: []string{domain.FQDN}, Status: "active",
		ActiveCertPath: "/managed/r1/fullchain.pem",
		ActiveKeyPath:  "/managed/r1/privkey.pem", Revision: 1,
	}
	if err := st.CreateProtocolCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeInboundTLSBinding(&store.NodeInboundTLSBinding{
		NodeID: node.ID, InboundID: managed.ID, Mode: "managed",
		ManagedDomainID: domain.ID, CertificateID: certificate.ID,
	}); err != nil {
		t.Fatal(err)
	}

	b := &Builder{Store: st}
	idx, err := b.LoadManagedTLSIndex()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []*store.InboundConfig{managed, plain} {
		want, wantHost, wantManaged, err := b.ResolveManagedTLS(node.ID, *in)
		if err != nil {
			t.Fatal(err)
		}
		got, gotHost, gotManaged, err := b.ResolveManagedTLSIndexed(idx, node.ID, *in)
		if err != nil {
			t.Fatal(err)
		}
		if gotManaged != wantManaged || gotHost != wantHost {
			t.Fatalf("inbound %s: indexed=(%v,%q) direct=(%v,%q)", in.Name, gotManaged, gotHost, wantManaged, wantHost)
		}
		if wantManaged && got.Params["tls_cert_path"] != want.Params["tls_cert_path"] {
			t.Fatalf("inbound %s: params diverge: %+v vs %+v", in.Name, got.Params, want.Params)
		}
	}
}
