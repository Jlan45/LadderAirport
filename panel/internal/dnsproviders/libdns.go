// Package dnsproviders contains built-in DNS provider adapters.
package dnsproviders

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/libdns/libdns"
)

type libdnsBackend interface {
	GetRecords(ctx context.Context, zone string) ([]libdns.Record, error)
	AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error)
	SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error)
	DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error)
}

type libdnsProvider struct {
	name        string
	defaultZone string
	backend     libdnsBackend
}

func (p *libdnsProvider) Test(ctx context.Context) error {
	if p.defaultZone == "" {
		return fmt.Errorf("DNS 账号尚未配置管理区域")
	}
	_, err := p.backend.GetRecords(ctx, libdnsZone(p.defaultZone))
	return p.wrap("测试连接", err)
}

func (p *libdnsProvider) ResolveZone(_ context.Context, fqdn string) (dnsprovider.Zone, error) {
	normalized, err := dnsprovider.NormalizeFQDN(fqdn)
	if err != nil {
		return dnsprovider.Zone{}, err
	}
	if p.defaultZone == "" {
		return dnsprovider.Zone{}, fmt.Errorf("必须明确提供 DNS 区域")
	}
	zone, err := dnsprovider.NormalizeFQDN(p.defaultZone)
	if err != nil {
		return dnsprovider.Zone{}, err
	}
	if normalized != zone && !strings.HasSuffix(normalized, "."+zone) {
		return dnsprovider.Zone{}, fmt.Errorf("域名 %s 不属于区域 %s", normalized, zone)
	}
	return dnsprovider.Zone{Name: zone}, nil
}

func (p *libdnsProvider) Lookup(
	ctx context.Context,
	zone dnsprovider.Zone,
	name string,
	typ dnsprovider.RecordType,
) ([]dnsprovider.Record, error) {
	records, err := p.backend.GetRecords(ctx, libdnsZone(zone.Name))
	if err != nil {
		return nil, p.wrap("查询记录", err)
	}
	name = normalizeRelative(name)
	out := []dnsprovider.Record{}
	for _, record := range records {
		converted, ok := fromLibdnsRecord(record)
		if !ok || normalizeRelative(converted.Name) != name || converted.Type != typ {
			continue
		}
		out = append(out, converted)
	}
	return out, nil
}

func (p *libdnsProvider) Upsert(
	ctx context.Context,
	zone dnsprovider.Zone,
	record dnsprovider.Record,
) (dnsprovider.RecordRef, error) {
	normalized, err := dnsprovider.NormalizeRecord(record, time.Minute, 24*time.Hour)
	if err != nil {
		return dnsprovider.RecordRef{}, err
	}
	libRecord, err := toLibdnsRecord(normalized)
	if err != nil {
		return dnsprovider.RecordRef{}, err
	}
	var records []libdns.Record
	if normalized.Type == dnsprovider.TypeTXT {
		existing, lookupErr := p.Lookup(ctx, zone, normalized.Name, normalized.Type)
		if lookupErr != nil {
			return dnsprovider.RecordRef{}, lookupErr
		}
		for _, record := range existing {
			if record.Value == normalized.Value {
				return dnsprovider.RecordRef{
					ID: record.ID, Name: record.Name, Type: record.Type, Value: record.Value,
				}, nil
			}
		}
		records, err = p.backend.AppendRecords(
			ctx, libdnsZone(zone.Name), []libdns.Record{libRecord},
		)
	} else {
		records, err = p.backend.SetRecords(
			ctx, libdnsZone(zone.Name), []libdns.Record{libRecord},
		)
	}
	if err != nil {
		return dnsprovider.RecordRef{}, p.wrap("写入记录", err)
	}
	if len(records) > 0 {
		if converted, ok := fromLibdnsRecord(records[0]); ok {
			return dnsprovider.RecordRef{
				ID: converted.ID, Name: converted.Name, Type: converted.Type, Value: converted.Value,
			}, nil
		}
	}
	return dnsprovider.RecordRef{
		Name: normalized.Name, Type: normalized.Type, Value: normalized.Value,
	}, nil
}

func (p *libdnsProvider) Delete(
	ctx context.Context,
	zone dnsprovider.Zone,
	ref dnsprovider.RecordRef,
) error {
	records, err := p.backend.GetRecords(ctx, libdnsZone(zone.Name))
	if err != nil {
		return p.wrap("删除前查询记录", err)
	}
	matches := []libdns.Record{}
	for _, record := range records {
		converted, ok := fromLibdnsRecord(record)
		if !ok ||
			normalizeRelative(converted.Name) != normalizeRelative(ref.Name) ||
			converted.Type != ref.Type {
			continue
		}
		if ref.Value != "" && converted.Value != ref.Value {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 0 {
		return nil
	}
	_, err = p.backend.DeleteRecords(ctx, libdnsZone(zone.Name), matches)
	return p.wrap("删除记录", err)
}

func (p *libdnsProvider) wrap(operation string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	kind := dnsprovider.ErrorTransient
	switch {
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"),
		strings.Contains(message, "invalidaccesskey"), strings.Contains(message, "authfail"):
		kind = dnsprovider.ErrorAuthentication
	case strings.Contains(message, "forbidden"), strings.Contains(message, "permission"),
		strings.Contains(message, "accessdenied"):
		kind = dnsprovider.ErrorPermission
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"),
		strings.Contains(message, "throttl"):
		kind = dnsprovider.ErrorRateLimit
	case strings.Contains(message, "invalid"), strings.Contains(message, "not found"):
		kind = dnsprovider.ErrorPermanent
	}
	return &dnsprovider.ProviderError{
		Kind: kind, Provider: p.name, Operation: operation,
		Message: "供应商 API 返回错误", Cause: err,
	}
}

func toLibdnsRecord(record dnsprovider.Record) (libdns.Record, error) {
	name := normalizeRelative(record.Name)
	switch record.Type {
	case dnsprovider.TypeA, dnsprovider.TypeAAAA:
		ip, err := netip.ParseAddr(record.Value)
		if err != nil {
			return nil, err
		}
		return libdns.Address{Name: name, TTL: record.TTL, IP: ip}, nil
	case dnsprovider.TypeTXT:
		return libdns.TXT{Name: name, TTL: record.TTL, Text: record.Value}, nil
	default:
		return nil, fmt.Errorf("不支持 DNS 记录类型：%s", record.Type)
	}
}

func fromLibdnsRecord(record libdns.Record) (dnsprovider.Record, bool) {
	switch value := record.(type) {
	case libdns.Address:
		typ := dnsprovider.TypeAAAA
		if value.IP.Is4() {
			typ = dnsprovider.TypeA
		}
		return dnsprovider.Record{
			Name: normalizeRelative(value.Name), Type: typ,
			Value: value.IP.String(), TTL: value.TTL,
		}, true
	case libdns.TXT:
		return dnsprovider.Record{
			Name: normalizeRelative(value.Name), Type: dnsprovider.TypeTXT,
			Value: value.Text, TTL: value.TTL,
		}, true
	default:
		rr := record.RR()
		parsed, err := rr.Parse()
		if err != nil {
			return dnsprovider.Record{}, false
		}
		switch typed := parsed.(type) {
		case libdns.Address:
			return fromLibdnsRecord(typed)
		case libdns.TXT:
			return fromLibdnsRecord(typed)
		default:
			return dnsprovider.Record{}, false
		}
	}
}

func normalizeRelative(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return "@"
	}
	return strings.ToLower(name)
}

func libdnsZone(zone string) string {
	zone = strings.TrimSpace(zone)
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	return zone
}
