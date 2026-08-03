// Package protocolcert manages Agent-local protocol certificate private keys.
package protocolcert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ladderairport/agent/internal/fileutil"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type Manager struct {
	root string
}

type Prepared struct {
	KeyID                string
	CSRPEM               string
	PublicKeyFingerprint string
}

type Installed struct {
	CertificatePath string
	KeyPath         string
	Fingerprint     string
	Serial          string
	NotBeforeUnix   int64
	NotAfterUnix    int64
}

type Status struct {
	KeyExists         bool
	CertificateExists bool
	KeyID             string
	CertificatePath   string
	KeyPath           string
	Fingerprint       string
	NotAfterUnix      int64
}

type metadata struct {
	KeyID        string   `json:"key_id"`
	DNSNames     []string `json:"dns_names"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	NotAfterUnix int64    `json:"not_after_unix,omitempty"`
}

func New(root string) (*Manager, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("必须提供协议证书目录")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("创建协议证书目录失败：%w", err)
	}
	return &Manager{root: root}, nil
}

func (m *Manager) Prepare(certificateID, generationID string, dnsNames []string) (Prepared, error) {
	dir, err := m.generationDir(certificateID, generationID)
	if err != nil {
		return Prepared{}, err
	}
	names, err := normalizeDNSNames(dnsNames)
	if err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Prepared{}, fmt.Errorf("创建协议证书代次目录失败：%w", err)
	}
	keyPath := filepath.Join(dir, "privkey.pem")
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return Prepared{}, err
	}
	request := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: names[0]},
		DNSNames: names,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, request, key)
	if err != nil {
		return Prepared{}, fmt.Errorf("生成协议证书 CSR 失败：%w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return Prepared{}, err
	}
	sum := sha256.Sum256(publicDER)
	keyID := certificateID + "/" + generationID
	if err := writeMetadata(filepath.Join(dir, "metadata.json"), metadata{
		KeyID: keyID, DNSNames: names,
	}); err != nil {
		return Prepared{}, err
	}
	return Prepared{
		KeyID:                keyID,
		CSRPEM:               string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})),
		PublicKeyFingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func (m *Manager) Install(
	certificateID, generationID, keyID, certificatePEM string,
	dnsNames []string,
	now time.Time,
) (Installed, error) {
	dir, err := m.generationDir(certificateID, generationID)
	if err != nil {
		return Installed{}, err
	}
	wantKeyID := certificateID + "/" + generationID
	if keyID != wantKeyID {
		return Installed{}, fmt.Errorf("协议证书密钥 ID 不匹配")
	}
	names, err := normalizeDNSNames(dnsNames)
	if err != nil {
		return Installed{}, err
	}
	keyPath := filepath.Join(dir, "privkey.pem")
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return Installed{}, fmt.Errorf("读取协议证书私钥失败：%w", err)
	}
	pair, err := tls.X509KeyPair([]byte(certificatePEM), keyPEM)
	if err != nil {
		return Installed{}, fmt.Errorf("协议证书与本地私钥不匹配：%w", err)
	}
	if len(pair.Certificate) == 0 {
		return Installed{}, fmt.Errorf("协议证书链为空")
	}
	certificates := make([]*x509.Certificate, 0, len(pair.Certificate))
	for _, raw := range pair.Certificate {
		certificate, err := x509.ParseCertificate(raw)
		if err != nil {
			return Installed{}, fmt.Errorf("解析协议证书链失败：%w", err)
		}
		certificates = append(certificates, certificate)
	}
	leaf := certificates[0]
	if now.IsZero() {
		now = time.Now()
	}
	if now.Add(5*time.Minute).Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return Installed{}, fmt.Errorf("协议证书不在有效期内")
	}
	if !serverAuthAllowed(leaf) {
		return Installed{}, fmt.Errorf("协议证书不允许 ServerAuth")
	}
	gotNames := append([]string(nil), leaf.DNSNames...)
	sort.Strings(gotNames)
	if !equalStrings(gotNames, names) {
		return Installed{}, fmt.Errorf("协议证书 DNS SAN 与申请不一致")
	}
	sum := sha256.Sum256(leaf.Raw)
	fingerprint := hex.EncodeToString(sum[:])
	certPath := filepath.Join(dir, "fullchain.pem")
	if err := fileutil.AtomicWrite(certPath, []byte(certificatePEM), 0o640); err != nil {
		return Installed{}, err
	}
	if err := writeMetadata(filepath.Join(dir, "metadata.json"), metadata{
		KeyID: keyID, DNSNames: names, Fingerprint: fingerprint,
		Serial: leaf.SerialNumber.Text(16), NotAfterUnix: leaf.NotAfter.Unix(),
	}); err != nil {
		return Installed{}, err
	}
	return Installed{
		CertificatePath: certPath,
		KeyPath:         keyPath,
		Fingerprint:     fingerprint,
		Serial:          leaf.SerialNumber.Text(16),
		NotBeforeUnix:   leaf.NotBefore.Unix(),
		NotAfterUnix:    leaf.NotAfter.Unix(),
	}, nil
}

func (m *Manager) Status(certificateID, generationID string) (Status, error) {
	dir, err := m.generationDir(certificateID, generationID)
	if err != nil {
		return Status{}, err
	}
	keyPath := filepath.Join(dir, "privkey.pem")
	certPath := filepath.Join(dir, "fullchain.pem")
	result := Status{
		KeyID:   certificateID + "/" + generationID,
		KeyPath: keyPath, CertificatePath: certPath,
	}
	if _, err := os.Stat(keyPath); err == nil {
		result.KeyExists = true
	} else if !os.IsNotExist(err) {
		return Status{}, err
	}
	if _, err := os.Stat(certPath); err == nil {
		result.CertificateExists = true
	} else if !os.IsNotExist(err) {
		return Status{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err == nil {
		var stored metadata
		if json.Unmarshal(raw, &stored) == nil {
			result.Fingerprint = stored.Fingerprint
			result.NotAfterUnix = stored.NotAfterUnix
		}
	}
	return result, nil
}

func (m *Manager) Delete(certificateID, generationID string) error {
	dir, err := m.generationDir(certificateID, generationID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除协议证书代次失败：%w", err)
	}
	return nil
}

func (m *Manager) generationDir(certificateID, generationID string) (string, error) {
	if !safeID.MatchString(certificateID) || !safeID.MatchString(generationID) {
		return "", fmt.Errorf("协议证书 ID 或代次 ID 无效")
	}
	return filepath.Join(m.root, certificateID, generationID), nil
}

func loadOrCreateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return parseKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成协议证书私钥失败：%w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		return parseKey(data)
	}
	if err != nil {
		return nil, fmt.Errorf("创建协议证书私钥失败：%w", err)
	}
	if _, err := file.Write(keyPEM); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

func parseKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("协议证书私钥 PEM 无效")
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析协议证书私钥失败：%w", err)
	}
	key, ok := value.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("协议证书私钥必须为 ECDSA P-256")
	}
	return key, nil
}

func normalizeDNSNames(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		value = strings.ToLower(strings.TrimSuffix(value, "."))
		if value == "" || strings.ContainsAny(value, `/\`) {
			return nil, fmt.Errorf("协议证书 DNS SAN 无效")
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("必须提供协议证书 DNS SAN")
	}
	sort.Strings(out)
	return out, nil
}

func serverAuthAllowed(certificate *x509.Certificate) bool {
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	return bytes.Equal([]byte(strings.Join(left, "\x00")), []byte(strings.Join(right, "\x00")))
}

func writeMetadata(path string, value metadata) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return fileutil.AtomicWrite(path, data, 0o600)
}
