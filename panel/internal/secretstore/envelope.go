// Package secretstore encrypts Panel-owned credentials and service tokens.
package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const envelopeVersion = "v1"

// Store is an AES-256-GCM credential envelope service.
type Store struct {
	aead cipher.AEAD
}

func New(key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("凭据主密钥必须为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化凭据加密失败：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化凭据 GCM 失败：%w", err)
	}
	return &Store{aead: aead}, nil
}

// Encrypt returns a versioned envelope. Associated data must be stable and
// target-specific, for example "dns-account:<id>:<provider>".
func (s *Store) Encrypt(plaintext []byte, associatedData string) (string, error) {
	if s == nil || s.aead == nil {
		return "", fmt.Errorf("凭据保险箱尚未初始化")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成凭据加密随机数失败：%w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, plaintext, []byte(associatedData))
	encoding := base64.RawURLEncoding
	return strings.Join([]string{
		envelopeVersion,
		encoding.EncodeToString(nonce),
		encoding.EncodeToString(ciphertext),
	}, "."), nil
}

func (s *Store) Decrypt(envelope, associatedData string) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, fmt.Errorf("凭据保险箱尚未初始化")
	}
	parts := strings.Split(envelope, ".")
	if len(parts) != 3 || parts[0] != envelopeVersion {
		return nil, fmt.Errorf("凭据密文格式或版本无效")
	}
	encoding := base64.RawURLEncoding
	nonce, err := encoding.DecodeString(parts[1])
	if err != nil || len(nonce) != s.aead.NonceSize() ||
		encoding.EncodeToString(nonce) != parts[1] {
		return nil, fmt.Errorf("凭据密文随机数无效")
	}
	ciphertext, err := encoding.DecodeString(parts[2])
	if err != nil || encoding.EncodeToString(ciphertext) != parts[2] {
		return nil, fmt.Errorf("凭据密文内容无效")
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte(associatedData))
	if err != nil {
		return nil, fmt.Errorf("凭据密文校验失败")
	}
	return plaintext, nil
}
