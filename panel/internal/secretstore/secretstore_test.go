package secretstore

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvelopeRoundTripAndAssociatedData(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	store, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := store.Encrypt([]byte(`{"token":"top-secret"}`), "dns-account:a:cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(envelope, "top-secret") {
		t.Fatal("envelope contains plaintext")
	}
	plaintext, err := store.Decrypt(envelope, "dns-account:a:cloudflare")
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != `{"token":"top-secret"}` {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := store.Decrypt(envelope, "dns-account:b:cloudflare"); err == nil {
		t.Fatal("decrypt with wrong associated data succeeded")
	}
}

func TestEnvelopeRejectsWrongKeyAndTamper(t *testing.T) {
	first, _ := New(bytes.Repeat([]byte{1}, 32))
	second, _ := New(bytes.Repeat([]byte{2}, 32))
	envelope, _ := first.Encrypt([]byte("secret"), "target")
	if _, err := second.Decrypt(envelope, "target"); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
	tampered := envelope[:len(envelope)-1] + "A"
	if _, err := first.Decrypt(tampered, "target"); err == nil {
		t.Fatal("tampered envelope succeeded")
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credentials.key")
	key, err := LoadOrCreateKey(KeyOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d", len(key))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	again, err := LoadOrCreateKey(KeyOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("reloaded key differs")
	}
}

func TestEnvironmentKeyTakesPrecedence(t *testing.T) {
	want := bytes.Repeat([]byte{9}, 32)
	key, err := LoadOrCreateKey(KeyOptions{
		EnvironmentValue: "hex:" + hex.EncodeToString(want),
		FilePath:         filepath.Join(t.TempDir(), "unused"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, want) {
		t.Fatal("parsed key differs")
	}
}

func TestRedaction(t *testing.T) {
	message := Redact("token=abc123 secret=long-abc123", "abc123", "long-abc123")
	if strings.Contains(message, "abc123") {
		t.Fatalf("secret remains in %q", message)
	}
	values := RedactMap(map[string]any{
		"api_token": "value",
		"zone_id":   "zone",
		"SecretId":  "identifier",
	})
	if values["api_token"] != redacted {
		t.Fatalf("api_token = %v", values["api_token"])
	}
	if values["zone_id"] != "zone" || values["SecretId"] != "identifier" {
		t.Fatalf("non-secret fields changed: %#v", values)
	}
}
