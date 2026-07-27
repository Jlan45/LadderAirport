package dnsproviders

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/libdns/libdns"
)

type memoryBackend struct {
	records []libdns.Record
}

func (m *memoryBackend) GetRecords(context.Context, string) ([]libdns.Record, error) {
	return append([]libdns.Record(nil), m.records...), nil
}

func (m *memoryBackend) SetRecords(_ context.Context, _ string, records []libdns.Record) ([]libdns.Record, error) {
	for _, incoming := range records {
		rr := incoming.RR()
		replaced := false
		for i, existing := range m.records {
			old := existing.RR()
			if old.Name == rr.Name && old.Type == rr.Type {
				m.records[i] = incoming
				replaced = true
				break
			}
		}
		if !replaced {
			m.records = append(m.records, incoming)
		}
	}
	return records, nil
}

func (m *memoryBackend) AppendRecords(_ context.Context, _ string, records []libdns.Record) ([]libdns.Record, error) {
	m.records = append(m.records, records...)
	return records, nil
}

func (m *memoryBackend) DeleteRecords(_ context.Context, _ string, records []libdns.Record) ([]libdns.Record, error) {
	for _, deleted := range records {
		rr := deleted.RR()
		for i := 0; i < len(m.records); i++ {
			if m.records[i].RR() == rr {
				m.records = append(m.records[:i], m.records[i+1:]...)
				i--
			}
		}
	}
	return records, nil
}

func TestLibdnsProviderCRUD(t *testing.T) {
	backend := &memoryBackend{records: []libdns.Record{
		libdns.Address{Name: "edge", TTL: time.Minute, IP: netip.MustParseAddr("192.0.2.1")},
		libdns.TXT{Name: "_acme-challenge.edge", TTL: time.Minute, Text: "other"},
	}}
	provider := &libdnsProvider{name: "memory", defaultZone: "example.com", backend: backend}
	zone, err := provider.ResolveZone(context.Background(), "edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.Lookup(context.Background(), zone, "edge", dnsprovider.TypeA)
	if err != nil || len(records) != 1 || records[0].Value != "192.0.2.1" {
		t.Fatalf("lookup = %+v, %v", records, err)
	}
	ref, err := provider.Upsert(context.Background(), zone, dnsprovider.Record{
		Name: "edge", Type: dnsprovider.TypeA, Value: "192.0.2.2", TTL: 300 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Value != "192.0.2.2" {
		t.Fatalf("ref = %+v", ref)
	}
	if err := provider.Delete(context.Background(), zone, ref); err != nil {
		t.Fatal(err)
	}
	records, err = provider.Lookup(context.Background(), zone, "edge", dnsprovider.TypeA)
	if err != nil || len(records) != 0 {
		t.Fatalf("records after delete = %+v, %v", records, err)
	}
	txtRef, err := provider.Upsert(context.Background(), zone, dnsprovider.Record{
		Name: "_acme-challenge.edge", Type: dnsprovider.TypeTXT,
		Value: "ours", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	txtRecords, err := provider.Lookup(
		context.Background(), zone, "_acme-challenge.edge", dnsprovider.TypeTXT,
	)
	if err != nil || len(txtRecords) != 2 {
		t.Fatalf("TXT append replaced concurrent value: %+v, %v", txtRecords, err)
	}
	if err := provider.Delete(context.Background(), zone, txtRef); err != nil {
		t.Fatal(err)
	}
	txtRecords, _ = provider.Lookup(
		context.Background(), zone, "_acme-challenge.edge", dnsprovider.TypeTXT,
	)
	if len(txtRecords) != 1 || txtRecords[0].Value != "other" {
		t.Fatalf("TXT cleanup removed unrelated value: %+v", txtRecords)
	}
}

func TestRegisterBuiltins(t *testing.T) {
	registry := dnsprovider.NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatal(err)
	}
	metadata := registry.Metadata()
	if len(metadata) != 3 {
		t.Fatalf("providers = %+v", metadata)
	}
	for _, provider := range metadata {
		if provider.Name == "cloudflare" &&
			(len(provider.CredentialFields) != 1 ||
				provider.CredentialFields[0].Name != "api_token") {
			t.Fatalf("Cloudflare credentials = %+v", provider.CredentialFields)
		}
	}
	if _, err := registry.New("cloudflare", dnsprovider.Config{}); err == nil {
		t.Fatal("cloudflare accepted missing token")
	}
}
