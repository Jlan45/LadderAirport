// Package dnsprovider defines vendor-neutral DNS record operations.
package dnsprovider

import (
	"context"
	"time"
)

type RecordType string

const (
	TypeA    RecordType = "A"
	TypeAAAA RecordType = "AAAA"
	TypeTXT  RecordType = "TXT"
)

type Zone struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

type Record struct {
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name"`
	Type  RecordType     `json:"type"`
	Value string         `json:"value"`
	TTL   time.Duration  `json:"ttl"`
	Extra map[string]any `json:"extra,omitempty"`
}

type RecordRef struct {
	ID    string     `json:"id,omitempty"`
	Name  string     `json:"name"`
	Type  RecordType `json:"type"`
	Value string     `json:"value,omitempty"`
}

type Provider interface {
	Test(ctx context.Context) error
	ResolveZone(ctx context.Context, fqdn string) (Zone, error)
	Lookup(ctx context.Context, zone Zone, name string, typ RecordType) ([]Record, error)
	Upsert(ctx context.Context, zone Zone, record Record) (RecordRef, error)
	Delete(ctx context.Context, zone Zone, ref RecordRef) error
}

type Config struct {
	Credentials map[string]string
	Settings    map[string]any
	Zone        string
	HTTPTimeout time.Duration
}

type Factory func(Config) (Provider, error)

type Metadata struct {
	Name             string            `json:"name"`
	Label            string            `json:"label"`
	CredentialFields []CredentialField `json:"credential_fields"`
	Capabilities     []RecordType      `json:"capabilities"`
}

type CredentialField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}
