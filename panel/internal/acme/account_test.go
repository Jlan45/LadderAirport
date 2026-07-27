package acme

import (
	"bytes"
	"testing"

	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
)

func TestAccountKeyEncryptedRoundTrip(t *testing.T) {
	secrets, err := secretstore.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := EncryptNewAccountKey(secrets, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Secrets: secrets}
	client, err := service.NewClient(&store.ACMEAccount{
		ID: "account-1", DirectoryURL: "https://acme.example/directory",
		AccountKeyCiphertext: ciphertext,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Key == nil || client.DirectoryURL != "https://acme.example/directory" {
		t.Fatalf("client = %+v", client)
	}
	if _, err := secrets.Decrypt(ciphertext, accountKeyAAD("another-account")); err == nil {
		t.Fatal("account key decrypted with wrong AAD")
	}
}

func TestDecodeEABKey(t *testing.T) {
	got, err := decodeEABKey([]byte("c2VjcmV0"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret" {
		t.Fatalf("key = %q", got)
	}
	if _, err := decodeEABKey([]byte("***")); err == nil {
		t.Fatal("invalid EAB key accepted")
	}
}
