package dnsprovider

import (
	"context"
	"fmt"
)

// DeleteTXTValue deletes only TXT records whose value exactly matches. It
// intentionally leaves concurrent ACME or operator TXT values untouched.
func DeleteTXTValue(ctx context.Context, provider Provider, zone Zone, name, value string) error {
	records, err := provider.Lookup(ctx, zone, name, TypeTXT)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Value != value {
			continue
		}
		if err := provider.Delete(ctx, zone, RecordRef{
			ID: record.ID, Name: record.Name, Type: record.Type, Value: record.Value,
		}); err != nil {
			return fmt.Errorf("删除 DNS TXT 验证值失败：%w", err)
		}
	}
	return nil
}
