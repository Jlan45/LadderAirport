package secretstore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type KeyOptions struct {
	// EnvironmentValue takes precedence over FilePath when non-empty.
	EnvironmentValue string
	FilePath         string
}

// LoadOrCreateKey loads a 32-byte key from the environment or a mode-0600
// file. A missing file is created atomically.
func LoadOrCreateKey(options KeyOptions) ([]byte, error) {
	if value := strings.TrimSpace(options.EnvironmentValue); value != "" {
		key, err := ParseKey(value)
		if err != nil {
			return nil, fmt.Errorf("解析 LADDER_CREDENTIALS_KEY 失败：%w", err)
		}
		return key, nil
	}
	if strings.TrimSpace(options.FilePath) == "" {
		return nil, fmt.Errorf("未设置凭据主密钥或密钥文件路径")
	}
	data, err := os.ReadFile(options.FilePath)
	if err == nil {
		if chmodErr := os.Chmod(options.FilePath, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("收紧凭据主密钥权限失败：%w", chmodErr)
		}
		return ParseKey(strings.TrimSpace(string(data)))
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取凭据主密钥失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(options.FilePath), 0o700); err != nil {
		return nil, fmt.Errorf("创建凭据主密钥目录失败：%w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成凭据主密钥失败：%w", err)
	}
	encoded := "hex:" + hex.EncodeToString(key) + "\n"
	file, err := os.OpenFile(options.FilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		// Another process won the creation race.
		return LoadOrCreateKey(options)
	}
	if err != nil {
		return nil, fmt.Errorf("创建凭据主密钥失败：%w", err)
	}
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("写入凭据主密钥失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("同步凭据主密钥失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("关闭凭据主密钥失败：%w", err)
	}
	return key, nil
}

// ParseKey accepts explicit hex:/base64: prefixes and unprefixed 64-character
// hex or standard/raw Base64.
func ParseKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	var (
		key []byte
		err error
	)
	switch {
	case strings.HasPrefix(value, "hex:"):
		key, err = hex.DecodeString(strings.TrimPrefix(value, "hex:"))
	case strings.HasPrefix(value, "base64:"):
		key, err = decodeBase64(strings.TrimPrefix(value, "base64:"))
	case len(value) == 64:
		key, err = hex.DecodeString(value)
	default:
		key, err = decodeBase64(value)
	}
	if err != nil {
		return nil, fmt.Errorf("主密钥编码无效")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("主密钥解码后必须为 32 字节")
	}
	return key, nil
}

func decodeBase64(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("base64 无效")
}
