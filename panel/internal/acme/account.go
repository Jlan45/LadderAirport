// Package acme implements ACME account and DNS-01 issuance without ever
// generating a protocol certificate private key on Panel.
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ladderairport/panel/internal/secretstore"
	"github.com/ladderairport/panel/internal/store"
	xacme "golang.org/x/crypto/acme"
)

type Service struct {
	Secrets    *secretstore.Store
	HTTPClient *http.Client
}

func GenerateAccountKeyPEM() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 ACME 账号密钥失败：%w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("编码 ACME 账号密钥失败：%w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func ParseAccountKeyPEM(value []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("ACME 账号密钥格式无效")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 ACME 账号密钥失败：%w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("ACME 账号密钥不支持签名")
	}
	return signer, nil
}

func (s *Service) NewClient(account *store.ACMEAccount) (*xacme.Client, error) {
	if s == nil || s.Secrets == nil || account == nil {
		return nil, fmt.Errorf("ACME 服务尚未初始化")
	}
	raw, err := s.Secrets.Decrypt(account.AccountKeyCiphertext, accountKeyAAD(account.ID))
	if err != nil {
		return nil, fmt.Errorf("解密 ACME 账号密钥失败：%w", err)
	}
	key, err := ParseAccountKeyPEM(raw)
	if err != nil {
		return nil, err
	}
	return &xacme.Client{
		Key: key, DirectoryURL: account.DirectoryURL,
		HTTPClient: s.HTTPClient, KID: xacme.KeyID(account.RegistrationURI),
		UserAgent: "LadderAirport",
	}, nil
}

func (s *Service) Register(ctx context.Context, account *store.ACMEAccount) (string, error) {
	client, err := s.NewClient(account)
	if err != nil {
		return "", err
	}
	acct := &xacme.Account{}
	if email := strings.TrimSpace(account.Email); email != "" {
		acct.Contact = []string{"mailto:" + email}
	}
	if account.EABKeyID != "" {
		raw, err := s.Secrets.Decrypt(account.EABHMACCiphertext, eabAAD(account.ID))
		if err != nil {
			return "", fmt.Errorf("解密 ACME EAB 密钥失败：%w", err)
		}
		key, err := decodeEABKey(raw)
		if err != nil {
			return "", err
		}
		acct.ExternalAccountBinding = &xacme.ExternalAccountBinding{
			KID: account.EABKeyID, Key: key,
		}
	}
	registered, err := client.Register(ctx, acct, func(string) bool {
		return account.TermsAcceptedUnix > 0
	})
	if errors.Is(err, xacme.ErrAccountAlreadyExists) {
		registered, err = client.GetReg(ctx, "")
	}
	if err != nil {
		return "", fmt.Errorf("注册 ACME 账号失败：%w", err)
	}
	if registered == nil || registered.URI == "" {
		return "", fmt.Errorf("ACME 服务未返回账号地址")
	}
	return registered.URI, nil
}

func EncryptNewAccountKey(secrets *secretstore.Store, accountID string) (string, error) {
	raw, err := GenerateAccountKeyPEM()
	if err != nil {
		return "", err
	}
	return secrets.Encrypt(raw, accountKeyAAD(accountID))
}

func EncryptEABKey(secrets *secretstore.Store, accountID, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return secrets.Encrypt([]byte(strings.TrimSpace(value)), eabAAD(accountID))
}

func accountKeyAAD(id string) string {
	return "acme-account-key:" + id
}

func eabAAD(id string) string {
	return "acme-eab-hmac:" + id
}

func decodeEABKey(raw []byte) ([]byte, error) {
	value := strings.TrimSpace(string(raw))
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(key) == 0 {
		return nil, fmt.Errorf("ACME EAB HMAC 必须是 Base64 编码")
	}
	return key, nil
}
