package dnsprovider

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

func NormalizeFQDN(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", fmt.Errorf("域名不能为空")
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil {
		return "", fmt.Errorf("域名 IDNA 转换失败：%w", err)
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 {
		return "", fmt.Errorf("域名长度超过 253 字节")
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("必须提供完整域名")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("域名标签无效：%q", label)
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("域名标签包含无效字符：%q", label)
			}
		}
	}
	return ascii, nil
}

// NormalizeRecordFQDN normalizes a DNS record owner name. Unlike host names,
// record owner labels may contain underscores (for example _acme-challenge).
// Labels without underscores still go through the same IDNA conversion used by
// NormalizeFQDN.
func NormalizeRecordFQDN(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	value = strings.NewReplacer("。", ".", "．", ".", "｡", ".").Replace(value)
	if value == "" {
		return "", fmt.Errorf("DNS 记录名称不能为空")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("必须提供完整 DNS 记录名称")
	}
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("DNS 记录名称标签无效：%q", label)
		}
		ascii := label
		if !strings.ContainsRune(label, '_') {
			var err error
			ascii, err = idna.Lookup.ToASCII(label)
			if err != nil {
				return "", fmt.Errorf("DNS 记录名称 IDNA 转换失败：%w", err)
			}
		}
		ascii = strings.ToLower(ascii)
		if len(ascii) > 63 ||
			strings.HasPrefix(ascii, "-") || strings.HasSuffix(ascii, "-") {
			return "", fmt.Errorf("DNS 记录名称标签无效：%q", label)
		}
		for _, char := range ascii {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') &&
				char != '-' && char != '_' {
				return "", fmt.Errorf("DNS 记录名称标签包含无效字符：%q", label)
			}
		}
		normalized = append(normalized, ascii)
	}
	result := strings.Join(normalized, ".")
	if len(result) > 253 {
		return "", fmt.Errorf("DNS 记录名称长度超过 253 字节")
	}
	return result, nil
}

func RelativeName(fqdn, zone string) (string, error) {
	normalizedFQDN, err := NormalizeRecordFQDN(fqdn)
	if err != nil {
		return "", err
	}
	normalizedZone, err := NormalizeFQDN(zone)
	if err != nil {
		return "", err
	}
	if normalizedFQDN == normalizedZone {
		return "@", nil
	}
	suffix := "." + normalizedZone
	if !strings.HasSuffix(normalizedFQDN, suffix) {
		return "", fmt.Errorf("域名 %s 不属于区域 %s", normalizedFQDN, normalizedZone)
	}
	return strings.TrimSuffix(normalizedFQDN, suffix), nil
}

func NormalizeRecord(record Record, minTTL, maxTTL time.Duration) (Record, error) {
	record.Name = strings.TrimSpace(strings.TrimSuffix(record.Name, "."))
	if record.Name == "" {
		record.Name = "@"
	}
	switch record.Type {
	case TypeA:
		address, err := netip.ParseAddr(strings.TrimSpace(record.Value))
		if err != nil || !address.Is4() {
			return Record{}, fmt.Errorf("A 记录值不是有效 IPv4")
		}
		record.Value = address.String()
	case TypeAAAA:
		address, err := netip.ParseAddr(strings.TrimSpace(record.Value))
		if err != nil || !address.Is6() {
			return Record{}, fmt.Errorf("AAAA 记录值不是有效 IPv6")
		}
		record.Value = address.String()
	case TypeTXT:
		if record.Value == "" {
			return Record{}, fmt.Errorf("TXT 记录值不能为空")
		}
	default:
		return Record{}, fmt.Errorf("不支持 DNS 记录类型：%s", record.Type)
	}
	if minTTL > 0 && record.TTL < minTTL {
		record.TTL = minTTL
	}
	if maxTTL > 0 && record.TTL > maxTTL {
		record.TTL = maxTTL
	}
	return record, nil
}
