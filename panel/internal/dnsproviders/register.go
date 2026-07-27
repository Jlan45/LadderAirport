package dnsproviders

import (
	"fmt"
	"strings"

	"github.com/ladderairport/panel/internal/dnsprovider"
	"github.com/libdns/alidns"
	"github.com/libdns/cloudflare"
	"github.com/libdns/tencentcloud"
)

func RegisterBuiltins(registry *dnsprovider.Registry) error {
	registrations := []struct {
		metadata dnsprovider.Metadata
		factory  dnsprovider.Factory
	}{
		{metadata: alidnsMetadata(), factory: newAliDNS},
		{metadata: dnspodMetadata(), factory: newDNSPod},
		{metadata: cloudflareMetadata(), factory: newCloudflare},
		{metadata: callbackMetadata(), factory: newCallback},
	}
	for _, registration := range registrations {
		if err := registry.Register(registration.metadata, registration.factory); err != nil {
			return err
		}
	}
	return nil
}

func newAliDNS(config dnsprovider.Config) (dnsprovider.Provider, error) {
	accessKeyID := strings.TrimSpace(config.Credentials["access_key_id"])
	accessKeySecret := strings.TrimSpace(config.Credentials["access_key_secret"])
	if accessKeyID == "" || accessKeySecret == "" {
		return nil, fmt.Errorf("必须提供 AccessKey ID 和 AccessKey Secret")
	}
	backend := &alidns.Provider{CredentialInfo: alidns.CredentialInfo{
		AccessKeyID:     accessKeyID,
		AccessKeySecret: accessKeySecret,
		RegionID:        strings.TrimSpace(config.Credentials["region_id"]),
		SecurityToken:   strings.TrimSpace(config.Credentials["security_token"]),
	}}
	return &libdnsProvider{
		name: "alidns", defaultZone: stringSetting(config.Settings, "test_zone"), backend: backend,
	}, nil
}

func newDNSPod(config dnsprovider.Config) (dnsprovider.Provider, error) {
	secretID := strings.TrimSpace(config.Credentials["secret_id"])
	secretKey := strings.TrimSpace(config.Credentials["secret_key"])
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("必须提供 SecretId 和 SecretKey")
	}
	backend := &tencentcloud.Provider{
		SecretId:     secretID,
		SecretKey:    secretKey,
		SessionToken: strings.TrimSpace(config.Credentials["session_token"]),
		Region:       strings.TrimSpace(config.Credentials["region"]),
	}
	return &libdnsProvider{
		name: "dnspod", defaultZone: stringSetting(config.Settings, "test_zone"), backend: backend,
	}, nil
}

func newCloudflare(config dnsprovider.Config) (dnsprovider.Provider, error) {
	apiToken := strings.TrimSpace(config.Credentials["api_token"])
	if apiToken == "" {
		return nil, fmt.Errorf("必须提供 Cloudflare API Token")
	}
	backend := &cloudflare.Provider{
		APIToken:  apiToken,
		ZoneToken: strings.TrimSpace(config.Credentials["zone_token"]),
	}
	return &libdnsProvider{
		name: "cloudflare", defaultZone: stringSetting(config.Settings, "test_zone"), backend: backend,
	}, nil
}

func alidnsMetadata() dnsprovider.Metadata {
	return dnsprovider.Metadata{
		Name: "alidns", Label: "阿里云 AliDNS",
		CredentialFields: []dnsprovider.CredentialField{
			{Name: "access_key_id", Label: "AccessKey ID", Required: true},
			{Name: "access_key_secret", Label: "AccessKey Secret", Secret: true, Required: true},
			{Name: "security_token", Label: "STS Security Token", Secret: true},
			{Name: "region_id", Label: "Region ID"},
		},
		Capabilities: []dnsprovider.RecordType{dnsprovider.TypeA, dnsprovider.TypeAAAA, dnsprovider.TypeTXT},
	}
}

func dnspodMetadata() dnsprovider.Metadata {
	return dnsprovider.Metadata{
		Name: "dnspod", Label: "腾讯云 DNSPod",
		CredentialFields: []dnsprovider.CredentialField{
			{Name: "secret_id", Label: "SecretId", Required: true},
			{Name: "secret_key", Label: "SecretKey", Secret: true, Required: true},
			{Name: "session_token", Label: "Session Token", Secret: true},
			{Name: "region", Label: "Region"},
		},
		Capabilities: []dnsprovider.RecordType{dnsprovider.TypeA, dnsprovider.TypeAAAA, dnsprovider.TypeTXT},
	}
}

func cloudflareMetadata() dnsprovider.Metadata {
	return dnsprovider.Metadata{
		Name: "cloudflare", Label: "Cloudflare DNS",
		CredentialFields: []dnsprovider.CredentialField{
			{Name: "api_token", Label: "API Token", Secret: true, Required: true},
			{Name: "zone_token", Label: "Zone Read Token", Secret: true},
		},
		Capabilities: []dnsprovider.RecordType{dnsprovider.TypeA, dnsprovider.TypeAAAA, dnsprovider.TypeTXT},
	}
}

func callbackMetadata() dnsprovider.Metadata {
	return dnsprovider.Metadata{
		Name: "callback", Label: "通用 HTTP Callback",
		CredentialFields: []dnsprovider.CredentialField{
			{
				Name: "token", Label: "Callback Token", Secret: true,
				Description: "可在 URL、Header 或 Body 中通过 #{credential.token} 引用",
			},
		},
		Capabilities: []dnsprovider.RecordType{dnsprovider.TypeA, dnsprovider.TypeAAAA, dnsprovider.TypeTXT},
	}
}

func stringSetting(settings map[string]any, key string) string {
	if settings == nil {
		return ""
	}
	value, _ := settings[key].(string)
	return strings.TrimSpace(value)
}
