package certmanager

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/ladderairport/panel/internal/secretstore"
)

type dnsPresenter struct {
	provider dnsprovider.Provider
	zone     dnsprovider.Zone
	timeout  time.Duration
	secrets  []string
}

func (p *dnsPresenter) Present(ctx context.Context, fqdn, value string) error {
	name, err := dnsprovider.RelativeName(fqdn, p.zone.Name)
	if err != nil {
		return err
	}
	_, err = p.provider.Upsert(ctx, p.zone, dnsprovider.Record{
		Name: name, Type: dnsprovider.TypeTXT, Value: value, TTL: time.Minute,
	})
	return p.redact(err)
}

func (p *dnsPresenter) Wait(ctx context.Context, fqdn, value string) error {
	name, err := dnsprovider.RelativeName(fqdn, p.zone.Name)
	if err != nil {
		return err
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		records, err := p.provider.Lookup(waitCtx, p.zone, name, dnsprovider.TypeTXT)
		if err == nil {
			for _, record := range records {
				if record.Value == value {
					visible, _ := authoritativeTXTVisible(waitCtx, p.zone.Name, fqdn, value)
					if visible {
						return nil
					}
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("TXT 记录 %s 在超时前未生效", fqdn)
		case <-ticker.C:
		}
	}
}

func authoritativeTXTVisible(ctx context.Context, zone, fqdn, value string) (bool, error) {
	nameservers, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil {
		return false, err
	}
	var lastErr error
	for _, nameserver := range nameservers {
		address := net.JoinHostPort(nameserver.Host, "53")
		for _, network := range []string{"udp", "tcp"} {
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(dialCtx, network, address)
				},
			}
			values, lookupErr := resolver.LookupTXT(ctx, fqdn)
			if lookupErr != nil {
				lastErr = lookupErr
				continue
			}
			for _, observed := range values {
				if observed == value {
					return true, nil
				}
			}
		}
	}
	return false, lastErr
}

func (p *dnsPresenter) Cleanup(ctx context.Context, fqdn, value string) error {
	name, err := dnsprovider.RelativeName(fqdn, p.zone.Name)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return p.redact(dnsprovider.DeleteTXTValue(cleanupCtx, p.provider, p.zone, name, value))
}

func (p *dnsPresenter) redact(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", secretstore.Redact(err.Error(), p.secrets...))
}
