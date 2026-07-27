package certmanager

import (
	"context"
	"testing"

	"github.com/ladderairport/panel/internal/dnsprovider"
)

type recordingDNSProvider struct {
	record dnsprovider.Record
}

func (*recordingDNSProvider) Test(context.Context) error { return nil }

func (*recordingDNSProvider) ResolveZone(context.Context, string) (dnsprovider.Zone, error) {
	return dnsprovider.Zone{Name: "example.com"}, nil
}

func (*recordingDNSProvider) Lookup(
	context.Context, dnsprovider.Zone, string, dnsprovider.RecordType,
) ([]dnsprovider.Record, error) {
	return nil, nil
}

func (p *recordingDNSProvider) Upsert(
	_ context.Context, _ dnsprovider.Zone, record dnsprovider.Record,
) (dnsprovider.RecordRef, error) {
	p.record = record
	return dnsprovider.RecordRef{
		Name: record.Name, Type: record.Type, Value: record.Value,
	}, nil
}

func (*recordingDNSProvider) Delete(
	context.Context, dnsprovider.Zone, dnsprovider.RecordRef,
) error {
	return nil
}

func TestDNSPresenterAcceptsACMEChallengeOwnerName(t *testing.T) {
	provider := &recordingDNSProvider{}
	presenter := &dnsPresenter{
		provider: provider,
		zone:     dnsprovider.Zone{Name: "example.com"},
	}
	if err := presenter.Present(
		context.Background(), "_acme-challenge.edge.example.com", "challenge-value",
	); err != nil {
		t.Fatal(err)
	}
	if provider.record.Name != "_acme-challenge.edge" ||
		provider.record.Type != dnsprovider.TypeTXT ||
		provider.record.Value != "challenge-value" {
		t.Fatalf("record = %+v", provider.record)
	}
}
