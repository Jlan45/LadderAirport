package secretstore

import (
	"fmt"
	"strings"
)

const redacted = "[已脱敏]"

// Redact replaces known non-empty secrets in a message. Longer values are
// replaced first so overlapping credentials cannot leave suffixes behind.
func Redact(message string, secrets ...string) string {
	values := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if value := strings.TrimSpace(secret); value != "" {
			values = append(values, value)
		}
	}
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if len(values[j]) > len(values[i]) {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	for _, secret := range values {
		message = strings.ReplaceAll(message, secret, redacted)
	}
	return message
}

// RedactMap returns a shallow copy with provider-aware secret keys replaced.
func RedactMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
		if isSecretKey(normalized) && fmt.Sprint(value) != "" {
			out[key] = redacted
			continue
		}
		out[key] = value
	}
	return out
}

func isSecretKey(key string) bool {
	switch key {
	case "secret", "secret_key", "access_key_secret", "api_secret",
		"api_token", "token", "authorization", "password", "hmac",
		"eab_hmac", "eab_hmac_key", "account_key", "private_key":
		return true
	default:
		return strings.HasSuffix(key, "_secret") || strings.HasSuffix(key, "_token")
	}
}
