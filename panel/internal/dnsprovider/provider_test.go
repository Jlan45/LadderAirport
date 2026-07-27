package dnsprovider

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeProvider struct {
	records []Record
	deleted []RecordRef
}

func (f *fakeProvider) Test(context.Context) error { return nil }
func (f *fakeProvider) ResolveZone(_ context.Context, fqdn string) (Zone, error) {
	return Zone{Name: "example.com"}, nil
}
func (f *fakeProvider) Lookup(_ context.Context, _ Zone, name string, typ RecordType) ([]Record, error) {
	out := []Record{}
	for _, record := range f.records {
		if record.Name == name && record.Type == typ {
			out = append(out, record)
		}
	}
	return out, nil
}
func (f *fakeProvider) Upsert(_ context.Context, _ Zone, record Record) (RecordRef, error) {
	f.records = append(f.records, record)
	return RecordRef{ID: record.ID, Name: record.Name, Type: record.Type, Value: record.Value}, nil
}
func (f *fakeProvider) Delete(_ context.Context, _ Zone, ref RecordRef) error {
	f.deleted = append(f.deleted, ref)
	return nil
}

func TestNormalizeFQDNAndRelativeName(t *testing.T) {
	got, err := NormalizeFQDN("  节点.Example.COM. ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "xn--3px729a.example.com" {
		t.Fatalf("NormalizeFQDN = %q", got)
	}
	relative, err := RelativeName(got, "example.com.")
	if err != nil {
		t.Fatal(err)
	}
	if relative != "xn--3px729a" {
		t.Fatalf("RelativeName = %q", relative)
	}
	apex, err := RelativeName("example.com", "example.com")
	if err != nil || apex != "@" {
		t.Fatalf("apex = %q, %v", apex, err)
	}
	challenge, err := RelativeName(
		"_acme-challenge.节点.Example.COM.", "example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if challenge != "_acme-challenge.xn--3px729a" {
		t.Fatalf("challenge = %q", challenge)
	}
	if _, err := NormalizeFQDN("_acme-challenge.example.com"); err == nil {
		t.Fatal("普通域名校验错误地接受了下划线")
	}
}

func TestNormalizeRecord(t *testing.T) {
	record, err := NormalizeRecord(Record{
		Type: TypeAAAA, Value: "2001:0db8::1", TTL: 10 * time.Second,
	}, 60*time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if record.Value != "2001:db8::1" || record.TTL != time.Minute || record.Name != "@" {
		t.Fatalf("record = %+v", record)
	}
	if _, err := NormalizeRecord(Record{Type: TypeA, Value: "::1"}, 0, 0); err == nil {
		t.Fatal("accepted IPv6 as A record")
	}
}

func TestRegistryAndProviderErrors(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(Metadata{Name: "fake", Label: "Fake"}, func(Config) (Provider, error) {
		return &fakeProvider{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.New("FAKE", Config{}); err != nil {
		t.Fatal(err)
	}
	if len(registry.Metadata()) != 1 {
		t.Fatal("metadata missing")
	}
	providerErr := &ProviderError{Kind: ErrorRateLimit, Provider: "fake", Cause: errors.New("429")}
	if !IsRetryable(providerErr) || !errors.Is(providerErr, providerErr.Cause) {
		t.Fatal("provider error classification failed")
	}
}

func TestDeleteTXTValueIsExact(t *testing.T) {
	provider := &fakeProvider{records: []Record{
		{ID: "one", Name: "_acme-challenge", Type: TypeTXT, Value: "wanted"},
		{ID: "two", Name: "_acme-challenge", Type: TypeTXT, Value: "other"},
	}}
	if err := DeleteTXTValue(
		context.Background(), provider, Zone{Name: "example.com"},
		"_acme-challenge", "wanted",
	); err != nil {
		t.Fatal(err)
	}
	if len(provider.deleted) != 1 || provider.deleted[0].ID != "one" {
		t.Fatalf("deleted = %+v", provider.deleted)
	}
}
