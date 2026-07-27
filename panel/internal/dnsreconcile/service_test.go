package dnsreconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
)

type memoryProvider struct {
	records map[dnsprovider.RecordType][]dnsprovider.Record
	deleted []dnsprovider.RecordRef
}

func (m *memoryProvider) Test(context.Context) error { return nil }
func (m *memoryProvider) ResolveZone(_ context.Context, fqdn string) (dnsprovider.Zone, error) {
	return dnsprovider.Zone{Name: "example.com"}, nil
}
func (m *memoryProvider) Lookup(_ context.Context, _ dnsprovider.Zone, _ string, typ dnsprovider.RecordType) ([]dnsprovider.Record, error) {
	return append([]dnsprovider.Record(nil), m.records[typ]...), nil
}
func (m *memoryProvider) Upsert(_ context.Context, _ dnsprovider.Zone, record dnsprovider.Record) (dnsprovider.RecordRef, error) {
	m.records[record.Type] = []dnsprovider.Record{record}
	return dnsprovider.RecordRef{Name: record.Name, Type: record.Type, Value: record.Value}, nil
}
func (m *memoryProvider) Delete(_ context.Context, _ dnsprovider.Zone, ref dnsprovider.RecordRef) error {
	m.deleted = append(m.deleted, ref)
	records := m.records[ref.Type]
	kept := records[:0]
	for _, record := range records {
		if record.Value != ref.Value {
			kept = append(kept, record)
		}
	}
	m.records[ref.Type] = kept
	return nil
}

func TestManualReconcile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "unknown"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	secrets, _ := secretstore.New(bytes.Repeat([]byte{7}, 32))
	credentialsJSON, _ := json.Marshal(map[string]string{"token": "secret"})
	account := &store.DNSAccount{
		Name: "memory", Provider: "memory", Zone: "example.com",
		Settings: map[string]any{}, Enabled: true,
	}
	account.ID = "account"
	account.CredentialsCiphertext, _ = secrets.Encrypt(credentialsJSON, "dns-account:account:memory")
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: account.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "manual",
		ManualIPv4: "192.0.2.10", TTL: 300, Enabled: true, State: "pending",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	domain.DesiredIPv6 = "2001:db8::10"
	domain.CreatedAAAAByPanel = true
	if err := st.UpdateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	provider := &memoryProvider{records: map[dnsprovider.RecordType][]dnsprovider.Record{
		dnsprovider.TypeAAAA: {{
			Name: "edge", Type: dnsprovider.TypeAAAA, Value: "2001:db8::10",
		}},
	}}
	registry := dnsprovider.NewRegistry()
	if err := registry.Register(dnsprovider.Metadata{Name: "memory"}, func(dnsprovider.Config) (dnsprovider.Provider, error) {
		return provider, nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	service := &Service{Store: st, Secrets: secrets, Providers: registry, Now: func() time.Time { return now }}
	if err := service.Reconcile(context.Background(), domain.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetManagedDomain(domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ready" || got.DesiredIPv4 != "192.0.2.10" ||
		len(got.ObservedIPv4) != 1 || !got.CreatedAByPanel ||
		got.NextReconcileUnix != now.Add(reconcileInterval).Unix() {
		t.Fatalf("domain = %+v", got)
	}
	if len(provider.records[dnsprovider.TypeA]) != 1 {
		t.Fatalf("provider records = %+v", provider.records)
	}
	if len(provider.records[dnsprovider.TypeAAAA]) != 0 ||
		got.CreatedAAAAByPanel || got.DesiredIPv6 != "" {
		t.Fatalf("disabled AAAA record was not cleaned: domain=%+v records=%+v", got, provider.records)
	}
}

func TestReconcileFailureSchedulesRetry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "10.0.0.1", GRPCPort: 50051, Status: "unknown"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	secrets, _ := secretstore.New(bytes.Repeat([]byte{8}, 32))
	raw, _ := json.Marshal(map[string]string{"token": "secret"})
	ciphertext, _ := secrets.Encrypt(raw, "dns-account:account:memory")
	account := &store.DNSAccount{
		ID: "account", Name: "memory", Provider: "memory", Zone: "example.com",
		CredentialsCiphertext: ciphertext, Settings: map[string]any{}, Enabled: true,
	}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: account.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "node_address",
		TTL: 300, Enabled: true, State: "pending",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	registry := dnsprovider.NewRegistry()
	_ = registry.Register(dnsprovider.Metadata{Name: "memory"}, func(dnsprovider.Config) (dnsprovider.Provider, error) {
		return &memoryProvider{records: map[dnsprovider.RecordType][]dnsprovider.Record{}}, nil
	})
	now := time.Unix(2_000_000_000, 0)
	service := &Service{Store: st, Secrets: secrets, Providers: registry, Now: func() time.Time { return now }}
	if err := service.Reconcile(context.Background(), domain.ID); err == nil {
		t.Fatal("private node address unexpectedly reconciled")
	}
	got, _ := st.GetManagedDomain(domain.ID)
	if got.State != "retry_wait" || got.RetryCount != 1 ||
		got.NextReconcileUnix != now.Add(time.Minute).Unix() {
		t.Fatalf("domain = %+v", got)
	}
}

func TestCleanupDeletesOnlyPanelCreatedExactValue(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	node := &store.Node{Name: "edge", Address: "192.0.2.10", GRPCPort: 50051, Status: "unknown"}
	if err := st.CreateNode(node); err != nil {
		t.Fatal(err)
	}
	secrets, _ := secretstore.New(bytes.Repeat([]byte{9}, 32))
	raw, _ := json.Marshal(map[string]string{"token": "secret"})
	ciphertext, _ := secrets.Encrypt(raw, "dns-account:account:memory")
	account := &store.DNSAccount{
		ID: "account", Name: "memory", Provider: "memory", Zone: "example.com",
		CredentialsCiphertext: ciphertext, Settings: map[string]any{}, Enabled: true,
	}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}
	domain := &store.ManagedDomain{
		NodeID: node.ID, DNSAccountID: account.ID, Zone: "example.com",
		FQDN: "edge.example.com", RecordMode: "a", AddressSource: "manual",
		ManualIPv4: "192.0.2.10", DesiredIPv4: "192.0.2.10",
		CreatedAByPanel: true, TTL: 300, Enabled: false, State: "deleting",
	}
	if err := st.CreateManagedDomain(domain); err != nil {
		t.Fatal(err)
	}
	provider := &memoryProvider{records: map[dnsprovider.RecordType][]dnsprovider.Record{
		dnsprovider.TypeA: {
			{Name: "edge", Type: dnsprovider.TypeA, Value: "192.0.2.10"},
			{Name: "edge", Type: dnsprovider.TypeA, Value: "192.0.2.11"},
		},
	}}
	registry := dnsprovider.NewRegistry()
	_ = registry.Register(dnsprovider.Metadata{Name: "memory"}, func(dnsprovider.Config) (dnsprovider.Provider, error) {
		return provider, nil
	})
	service := &Service{Store: st, Secrets: secrets, Providers: registry}
	if err := service.Cleanup(context.Background(), domain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetManagedDomain(domain.ID); err == nil {
		t.Fatal("cleanup did not remove managed domain")
	}
	if len(provider.deleted) != 1 || provider.deleted[0].Value != "192.0.2.10" {
		t.Fatalf("deleted refs = %+v", provider.deleted)
	}
	if got := provider.records[dnsprovider.TypeA]; len(got) != 1 || got[0].Value != "192.0.2.11" {
		t.Fatalf("unmanaged DNS value was changed: %+v", got)
	}
}
