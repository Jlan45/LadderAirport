package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ladderairport/panel/internal/api"
	"github.com/ladderairport/panel/internal/store"
	agentv1 "github.com/ladderairport/proto/gen/go/agent/v1"
)

type frpsMockLive struct {
	*mockLive
	lastConfig *agentv1.FRPServerConfig
	lastHash   string
	state      string
}

func (m *frpsMockLive) ApplyFRPServerConfig(
	_ context.Context,
	config *agentv1.FRPServerConfig,
	hash string,
) (*agentv1.ApplyFRPServerConfigResponse, error) {
	m.lastConfig = config
	m.lastHash = hash
	if config.GetEnabled() {
		m.state = "running"
	} else {
		m.state = "stopped"
	}
	return &agentv1.ApplyFRPServerConfigResponse{
		Ok: true, Message: "applied", AppliedHash: hash,
	}, nil
}

func (m *frpsMockLive) StartFRPServer(context.Context) (*agentv1.StartFRPServerResponse, error) {
	m.state = "running"
	return &agentv1.StartFRPServerResponse{Ok: true, Message: "started"}, nil
}

func (m *frpsMockLive) StopFRPServer(context.Context) (*agentv1.StopFRPServerResponse, error) {
	m.state = "stopped"
	return &agentv1.StopFRPServerResponse{Ok: true, Message: "stopped"}, nil
}

func (m *frpsMockLive) GetFRPServerStatus(context.Context) (*agentv1.GetFRPServerStatusResponse, error) {
	return &agentv1.GetFRPServerStatusResponse{
		State: m.state, ConfigHash: m.lastHash, FrpsVersion: "0.69.0",
		StartedAtUnix: 123,
	}, nil
}

func (m *frpsMockLive) GetFRPServerMappings(context.Context) (*agentv1.GetFRPServerMappingsResponse, error) {
	return &agentv1.GetFRPServerMappingsResponse{
		CollectedAtUnix: 456,
		Clients: []*agentv1.FRPServerClient{{
			Key: "edge-a", ClientId: "edge-a", Hostname: "nas-a",
			ClientIp: "198.51.100.8:41230", Version: "0.69.0", Online: true,
		}},
		Mappings: []*agentv1.FRPServerMapping{{
			Name: "edge-a.ssh", Type: "tcp", Status: "online", ClientId: "edge-a",
			LocalIp: "127.0.0.1", LocalPort: 22, RemotePort: 22022,
			CurrentConnections: 2, TrafficInBytes: 1024, TrafficOutBytes: 2048,
		}},
	}, nil
}

func TestNodeFRPSConfigEncryptsAndPreservesToken(t *testing.T) {
	live := &frpsMockLive{mockLive: &mockLive{pingOK: true}, state: "stopped"}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()

	node := &store.Node{
		Name: "frps-node", Address: "127.0.0.1", GRPCPort: 50051,
		Status: "online", Capabilities: []string{"frps-v1", "frps-mappings-v1"},
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"enabled": true, "bind_addr": "0.0.0.0", "bind_port": 7000,
		"proxy_bind_addr": "0.0.0.0",
		"allow_ports":     []map[string]int{{"start": 20000, "end": 20100}},
		"auth_token":      "frps-secret-token", "tls_force": true,
		"max_ports_per_client": 8,
	}
	response, payload := doJSON(
		t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", body,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, response = %#v", response.StatusCode, payload)
	}
	if payload["has_auth_token"] != true {
		t.Fatalf("has_auth_token = %#v", payload["has_auth_token"])
	}
	if _, exposed := payload["auth_token"]; exposed {
		t.Fatal("FRPS auth token must not be returned")
	}
	stored, err := st.GetFRPServerConfig(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AuthTokenCiphertext == "" ||
		strings.Contains(stored.AuthTokenCiphertext, "frps-secret-token") {
		t.Fatalf("token was not encrypted: %q", stored.AuthTokenCiphertext)
	}
	if live.lastConfig.GetAuthToken() != "frps-secret-token" {
		t.Fatalf("agent token = %q", live.lastConfig.GetAuthToken())
	}

	delete(body, "auth_token")
	body["max_ports_per_client"] = 9
	response, payload = doJSON(
		t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", body,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("second PUT status = %d, response = %#v", response.StatusCode, payload)
	}
	if live.lastConfig.GetAuthToken() != "frps-secret-token" {
		t.Fatalf("preserved agent token = %q", live.lastConfig.GetAuthToken())
	}

	response, payload = doJSON(
		t, client, http.MethodGet,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps/mappings", nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mappings status = %d, response = %#v", response.StatusCode, payload)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("mappings Cache-Control = %q", response.Header.Get("Cache-Control"))
	}
	if payload["collected_at_unix"] != float64(456) {
		t.Fatalf("collected_at_unix = %#v", payload["collected_at_unix"])
	}
	mappings, _ := payload["mappings"].([]any)
	if len(mappings) != 1 {
		t.Fatalf("mappings = %#v", payload["mappings"])
	}
}

func TestNodeFRPSRejectsUnsafeConfiguration(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := &store.Node{Name: "frps-node", Address: "127.0.0.1", GRPCPort: 50051}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	response, payload := doJSON(t, client, http.MethodPut,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps", map[string]any{
			"enabled": true, "bind_addr": "0.0.0.0", "bind_port": 65536,
			"proxy_bind_addr": "0.0.0.0",
			"allow_ports":     []map[string]int{{"start": 20000, "end": 20100}},
		})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, response = %#v", response.StatusCode, payload)
	}
}

func TestNodeFRPSAcceptsPrivilegedPorts(t *testing.T) {
	live := &frpsMockLive{mockLive: &mockLive{pingOK: true}, state: "stopped"}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := &store.Node{
		Name: "frps-privileged", Address: "127.0.0.1", GRPCPort: 50051,
		Status: "online", Capabilities: []string{"frps-v1"},
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	response, payload := doJSON(t, client, http.MethodPut,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps", map[string]any{
			"enabled": true, "bind_addr": "0.0.0.0", "bind_port": 80,
			"proxy_bind_addr": "0.0.0.0",
			"allow_ports":     []map[string]int{{"start": 443, "end": 443}},
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, response = %#v", response.StatusCode, payload)
	}
	if live.lastConfig.GetBindPort() != 80 {
		t.Fatalf("agent bind port = %d, want 80", live.lastConfig.GetBindPort())
	}
	if len(live.lastConfig.GetAllowPorts()) != 1 ||
		live.lastConfig.GetAllowPorts()[0].GetStart() != 443 ||
		live.lastConfig.GetAllowPorts()[0].GetEnd() != 443 {
		t.Fatalf("allow ports = %+v", live.lastConfig.GetAllowPorts())
	}
}

func TestNodeFRPSAutoFillsSafeDefaultsAndToken(t *testing.T) {
	live := &frpsMockLive{mockLive: &mockLive{pingOK: true}, state: "stopped"}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := &store.Node{
		Name: "auto-frps", Address: "127.0.0.1", GRPCPort: 50051,
		Status: "online", Capabilities: []string{"frps-v1"},
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}

	response, payload := doJSON(t, client, http.MethodPut,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps", map[string]any{"enabled": true})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, response = %#v", response.StatusCode, payload)
	}
	generated, _ := payload["generated_auth_token"].(string)
	if generated == "" || live.lastConfig.GetAuthToken() != generated {
		t.Fatalf("generated token = %q, agent token = %q", generated, live.lastConfig.GetAuthToken())
	}
	if live.lastConfig.GetBindPort() != 7000 || !live.lastConfig.GetTlsForce() {
		t.Fatalf("agent defaults = %+v", live.lastConfig)
	}
	if len(live.lastConfig.GetAllowPorts()) != 1 ||
		live.lastConfig.GetAllowPorts()[0].GetStart() != 20000 ||
		live.lastConfig.GetAllowPorts()[0].GetEnd() != 30000 {
		t.Fatalf("allow ports = %+v", live.lastConfig.GetAllowPorts())
	}
	if live.lastConfig.GetMaxPortsPerClient() != 8 {
		t.Fatalf("max ports = %d", live.lastConfig.GetMaxPortsPerClient())
	}

	response, payload = doJSON(t, client, http.MethodGet,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", response.StatusCode)
	}
	if _, exposed := payload["generated_auth_token"]; exposed {
		t.Fatal("generated token must only be returned once")
	}

	response, payload = doJSON(t, client, http.MethodPost,
		ts.URL+"/api/v1/nodes/"+node.ID+"/frps/token/reveal", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reveal status = %d, response = %#v", response.StatusCode, payload)
	}
	if payload["auth_token"] != generated {
		t.Fatalf("revealed token = %q, want %q", payload["auth_token"], generated)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header.Get("Cache-Control"))
	}
}

func createFRPSTestDomain(t *testing.T, st *store.Store, nodeID, fqdn string, enabled bool) *store.ManagedDomain {
	t.Helper()
	account := &store.DNSAccount{
		ID: "acct-" + nodeID, Name: "acct-" + nodeID, Provider: "memory",
		Zone: "example.com", Enabled: true,
	}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: nodeID, DNSAccountID: account.ID, Zone: "example.com",
		FQDN: fqdn, RecordMode: "a", AddressSource: "agent_public",
		Enabled: enabled, State: "ready",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	return domain
}

func TestNodeFRPSManagedDomainBinding(t *testing.T) {
	live := &frpsMockLive{mockLive: &mockLive{pingOK: true}, state: "stopped"}
	ts, client, st := newTestServer(t, nil, func(
		context.Context, store.Node, string,
	) (api.NodeLive, error) {
		return live, nil
	})
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := &store.Node{
		Name: "frps-node", Address: "192.0.2.20", GRPCPort: 50051,
		Status: "online", Capabilities: []string{"frps-v1"},
	}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	domain := createFRPSTestDomain(t, st, node.ID, "frps.example.com", true)

	body := map[string]any{
		"enabled": true, "auth_token": "frps-secret-token",
		"managed_domain_id": domain.ID,
	}
	response, payload := doJSON(
		t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", body,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, response = %#v", response.StatusCode, payload)
	}
	if payload["managed_domain_id"] != domain.ID {
		t.Fatalf("managed_domain_id = %#v", payload["managed_domain_id"])
	}
	if payload["server_addr"] != "frps.example.com" {
		t.Fatalf("server_addr = %#v, want bound fqdn", payload["server_addr"])
	}

	response, payload = doJSON(
		t, client, http.MethodGet, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", nil,
	)
	if response.StatusCode != http.StatusOK || payload["server_addr"] != "frps.example.com" {
		t.Fatalf("GET status = %d, server_addr = %#v", response.StatusCode, payload["server_addr"])
	}

	// Clearing the binding falls back to the node address for display only.
	body["managed_domain_id"] = ""
	response, payload = doJSON(
		t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", body,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unbind PUT status = %d, response = %#v", response.StatusCode, payload)
	}
	if _, bound := payload["managed_domain_id"]; bound {
		t.Fatalf("managed_domain_id should be cleared: %#v", payload)
	}
	if payload["server_addr"] != "192.0.2.20" {
		t.Fatalf("server_addr = %#v, want node address fallback", payload["server_addr"])
	}
}

func TestNodeFRPSManagedDomainBindingValidation(t *testing.T) {
	ts, client, st := newTestServer(t, nil, nil)
	resp := login(t, client, ts.URL, "admin")
	resp.Body.Close()
	node := &store.Node{Name: "frps-node", Address: "192.0.2.20", GRPCPort: 50051}
	other := &store.Node{Name: "other-node", Address: "192.0.2.30", GRPCPort: 50051}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode(other); err != nil {
		t.Fatal(err)
	}
	otherDomain := createFRPSTestDomain(t, st, other.ID, "other.example.com", true)
	disabledDomain := createFRPSTestDomain(t, st, node.ID, "disabled.example.com", false)

	cases := map[string]map[string]any{
		"cross node":    {"enabled": false, "managed_domain_id": otherDomain.ID},
		"missing":       {"enabled": false, "managed_domain_id": "no-such-domain"},
		"disabled fqdn": {"enabled": false, "managed_domain_id": disabledDomain.ID},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response, payload := doJSON(
				t, client, http.MethodPut, ts.URL+"/api/v1/nodes/"+node.ID+"/frps", body,
			)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, response = %#v", response.StatusCode, payload)
			}
		})
	}
}
