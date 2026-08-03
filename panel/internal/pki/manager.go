// Package pki implements the LadderAirport management-plane certificate
// authority. It is intentionally separate from proxy inbound/public ACME TLS.
package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	AgentProfile = "agent-server"
	PanelProfile = "panel-client"

	agentLifetime = 30 * 24 * time.Hour
)

type Manager struct {
	dir          string
	root         *x509.Certificate
	intermediate *x509.Certificate
	signer       crypto.Signer
	bundlePEM    []byte
	clientMu     sync.RWMutex
	clientCert   tls.Certificate
	// AuditLog, when set, persists CA lifecycle warnings (e.g. intermediate
	// expiry) via the Panel audit mechanism.
	AuditLog func(action, nodeID, serial, actor, detail string) error
	// lastExpiryWarning rate-limits expiry warnings (Run goroutine only).
	lastExpiryWarning time.Time
}

type IssuedCertificate struct {
	Serial      string
	Certificate *x509.Certificate
	CertPEM     []byte // leaf followed by intermediate
	CABundlePEM []byte // root trust anchor followed by intermediate
}

type Status struct {
	Enabled                  bool   `json:"enabled"`
	RootSubject              string `json:"root_subject"`
	RootNotAfterUnix         int64  `json:"root_not_after_unix"`
	IntermediateSubject      string `json:"intermediate_subject"`
	IntermediateNotAfterUnix int64  `json:"intermediate_not_after_unix"`
	AgentLifetimeSeconds     int64  `json:"agent_lifetime_seconds"`
	Directory                string `json:"directory"`
	RootKeyOnline            bool   `json:"root_key_online"`
}

// Open loads a CA from dir, bootstrapping a root, online intermediate and
// Panel client identity when the directory is empty.
func Open(dir string) (*Manager, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("必须提供 PKI 目录")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建 PKI 目录失败：%w", err)
	}
	rootCertPath := filepath.Join(dir, "root-ca.crt")
	rootKeyPath := filepath.Join(dir, "offline", "root-ca.key")
	intCertPath := filepath.Join(dir, "intermediate-ca.crt")
	intKeyPath := filepath.Join(dir, "intermediate-ca.key")
	if !allExist(rootCertPath, intCertPath, intKeyPath) {
		if anyExist(rootCertPath, intCertPath, intKeyPath) {
			return nil, fmt.Errorf("%s 中的 PKI 材料不完整", dir)
		}
		if err := bootstrap(rootCertPath, rootKeyPath, intCertPath, intKeyPath); err != nil {
			return nil, err
		}
	}

	root, err := loadCertificate(rootCertPath)
	if err != nil {
		return nil, err
	}
	intermediate, err := loadCertificate(intCertPath)
	if err != nil {
		return nil, err
	}
	signer, err := loadSigner(intKeyPath)
	if err != nil {
		return nil, err
	}
	if !root.IsCA || !intermediate.IsCA {
		return nil, fmt.Errorf("根证书和中间证书必须是 CA 证书")
	}
	if err := intermediate.CheckSignatureFrom(root); err != nil {
		return nil, fmt.Errorf("中间 CA 不是由根 CA 签发：%w", err)
	}
	if !publicKeysEqual(intermediate.PublicKey, signer.Public()) {
		return nil, fmt.Errorf("中间 CA 证书与私钥不匹配")
	}
	rootPEM, err := os.ReadFile(rootCertPath)
	if err != nil {
		return nil, err
	}
	intPEM, err := os.ReadFile(intCertPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		dir:          dir,
		root:         root,
		intermediate: intermediate,
		signer:       signer,
		bundlePEM:    append(append([]byte{}, rootPEM...), intPEM...),
	}
	if err := m.ensurePanelClient(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) BundlePEM() []byte {
	return append([]byte{}, m.bundlePEM...)
}

func (m *Manager) ClientCertificate() tls.Certificate {
	m.clientMu.RLock()
	defer m.clientMu.RUnlock()
	return m.clientCert
}

// intermediateExpiryWarning triggers an alert when the online intermediate CA
// has less than this lifetime remaining. Rotation stays operator-driven.
const intermediateExpiryWarning = 30 * 24 * time.Hour

// Run renews the Panel client identity without requiring a Panel restart.
// It also warns when the intermediate CA approaches expiry.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.warnIntermediateExpiry(time.Now())
			current := m.ClientCertificate()
			if current.Leaf != nil && time.Until(current.Leaf.NotAfter) > 7*24*time.Hour {
				continue
			}
			if err := m.rotatePanelClient(time.Now()); err != nil {
				log.Printf("管理 PKI：续签 Panel 客户端证书失败：%v", err)
			}
		}
	}
}

// warnIntermediateExpiry logs (and audits, at most once per 24h) when the
// intermediate CA expires within intermediateExpiryWarning.
func (m *Manager) warnIntermediateExpiry(now time.Time) {
	remaining := time.Until(m.intermediate.NotAfter)
	if remaining > intermediateExpiryWarning {
		return
	}
	if now.Sub(m.lastExpiryWarning) < 24*time.Hour {
		return
	}
	m.lastExpiryWarning = now
	detail := fmt.Sprintf("中间 CA 到期时间=%s，剩余=%s", m.intermediate.NotAfter.UTC().Format(time.RFC3339), remaining.Truncate(time.Minute))
	log.Printf("管理 PKI：%s，请尽快离线轮换（-pki-rotate-intermediate）", detail)
	if m.AuditLog != nil {
		_ = m.AuditLog("intermediate.expiry-warning", "", "", "panel", detail)
	}
}

func (m *Manager) Status() Status {
	_, err := os.Stat(filepath.Join(m.dir, "offline", "root-ca.key"))
	return Status{
		Enabled:                  true,
		RootSubject:              m.root.Subject.String(),
		RootNotAfterUnix:         m.root.NotAfter.Unix(),
		IntermediateSubject:      m.intermediate.Subject.String(),
		IntermediateNotAfterUnix: m.intermediate.NotAfter.Unix(),
		AgentLifetimeSeconds:     int64(agentLifetime.Seconds()),
		Directory:                m.dir,
		RootKeyOnline:            err == nil,
	}
}

// RotateIntermediate signs a fresh online intermediate with the offline root
// and immediately rotates the Panel client identity. Existing Agent chains
// remain valid because the root trust anchor is unchanged.
func (m *Manager) RotateIntermediate(now time.Time) error {
	rootKeyPath := filepath.Join(m.dir, "offline", "root-ca.key")
	rootSigner, err := loadSigner(rootKeyPath)
	if err != nil {
		return fmt.Errorf("加载离线根 CA 私钥失败：%w", err)
	}
	if !publicKeysEqual(m.root.PublicKey, rootSigner.Public()) {
		return fmt.Errorf("根 CA 证书与私钥不匹配")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _, err := newSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "LadderAirport Management Intermediate CA",
			Organization: []string{"LadderAirport"},
		},
		NotBefore:             now.UTC().Add(-5 * time.Minute),
		NotAfter:              now.UTC().AddDate(2, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, m.root, key.Public(), rootSigner)
	if err != nil {
		return fmt.Errorf("签发中间 CA 失败：%w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(m.dir, "intermediate-ca.key")
	certPath := filepath.Join(m.dir, "intermediate-ca.crt")
	if err := writePrivateKey(keyPath, key); err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := atomicWrite(certPath, certPEM, 0o644); err != nil {
		return err
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.root.Raw})
	m.intermediate = cert
	m.signer = key
	m.bundlePEM = append(rootPEM, certPEM...)
	return m.rotatePanelClient(now)
}

func AgentURI(nodeID string) string {
	return "spiffe://ladderairport/agent/" + nodeID
}

func PanelURI() string {
	return "spiffe://ladderairport/panel/control"
}

func (m *Manager) SignAgentCSR(nodeID string, csrPEM []byte, now time.Time) (*IssuedCertificate, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.ContainsAny(nodeID, "/?#") {
		return nil, fmt.Errorf("节点 ID 无效")
	}
	csr, err := parseAndValidateCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	if len(csr.DNSNames)+len(csr.IPAddresses) == 0 {
		return nil, fmt.Errorf("Agent CSR 至少需要一个 DNS 或 IP SAN")
	}
	if len(csr.DNSNames)+len(csr.IPAddresses) > 32 {
		return nil, fmt.Errorf("SAN 数量过多")
	}
	uri, _ := url.Parse(AgentURI(nodeID))
	return m.issue(pkix.Name{
		CommonName:   nodeID,
		Organization: []string{"LadderAirport Agents"},
	}, csr.PublicKey, AgentProfile, []*url.URL{uri}, csr.DNSNames, csr.IPAddresses, now, agentLifetime)
}

func (m *Manager) issue(subject pkix.Name, publicKey any, profile string, uris []*url.URL, dns []string, ips []net.IP, now time.Time, lifetime time.Duration) (*IssuedCertificate, error) {
	serial, serialHex, err := newSerial()
	if err != nil {
		return nil, err
	}
	notBefore := now.UTC().Add(-5 * time.Minute)
	notAfter := now.UTC().Add(lifetime)
	if max := m.intermediate.NotAfter.Add(-time.Hour); notAfter.After(max) {
		notAfter = max
	}
	if !notAfter.After(notBefore) {
		return nil, fmt.Errorf("中间 CA 即将过期，无法签发证书")
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string{}, dns...),
		IPAddresses:           append([]net.IP{}, ips...),
		URIs:                  uris,
	}
	if profile == PanelProfile {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.intermediate, publicKey, m.signer)
	if err != nil {
		return nil, fmt.Errorf("签发证书失败：%w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	intPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.intermediate.Raw})
	return &IssuedCertificate{
		Serial:      serialHex,
		Certificate: cert,
		CertPEM:     append(leafPEM, intPEM...),
		CABundlePEM: m.BundlePEM(),
	}, nil
}

func (m *Manager) ensurePanelClient() error {
	certPath := filepath.Join(m.dir, "panel-client.crt")
	keyPath := filepath.Join(m.dir, "panel-client.key")
	renew := true
	if pair, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(pair.Certificate) > 0 {
		leaf, parseErr := x509.ParseCertificate(pair.Certificate[0])
		if parseErr == nil && time.Until(leaf.NotAfter) > 7*24*time.Hour {
			pair.Leaf = leaf
			m.setClientCertificate(pair)
			renew = false
		}
	}
	if !renew {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	uri, _ := url.Parse(PanelURI())
	issued, err := m.issue(pkix.Name{
		CommonName:   "ladder-panel",
		Organization: []string{"LadderAirport Control Plane"},
	}, key.Public(), PanelProfile, []*url.URL{uri}, nil, nil, time.Now(), 90*24*time.Hour)
	if err != nil {
		return err
	}
	if err := writePrivateKey(keyPath, key); err != nil {
		return err
	}
	if err := atomicWrite(certPath, issued.CertPEM, 0o644); err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}
	pair.Leaf = issued.Certificate
	m.setClientCertificate(pair)
	return nil
}

func (m *Manager) rotatePanelClient(now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	uri, _ := url.Parse(PanelURI())
	issued, err := m.issue(pkix.Name{
		CommonName:   "ladder-panel",
		Organization: []string{"LadderAirport Control Plane"},
	}, key.Public(), PanelProfile, []*url.URL{uri}, nil, nil, now, 90*24*time.Hour)
	if err != nil {
		return err
	}
	keyPath := filepath.Join(m.dir, "panel-client.key")
	certPath := filepath.Join(m.dir, "panel-client.crt")
	if err := writePrivateKey(keyPath, key); err != nil {
		return err
	}
	if err := atomicWrite(certPath, issued.CertPEM, 0o644); err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}
	pair.Leaf = issued.Certificate
	m.setClientCertificate(pair)
	return nil
}

func (m *Manager) setClientCertificate(cert tls.Certificate) {
	m.clientMu.Lock()
	m.clientCert = cert
	m.clientMu.Unlock()
}

func bootstrap(rootCertPath, rootKeyPath, intCertPath, intKeyPath string) error {
	now := time.Now().UTC()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	rootSerial, _, err := newSerial()
	if err != nil {
		return err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber: rootSerial,
		Subject: pkix.Name{
			CommonName:   "LadderAirport Management Root CA",
			Organization: []string{"LadderAirport"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, rootKey.Public(), rootKey)
	if err != nil {
		return err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return err
	}
	intKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	intSerial, _, err := newSerial()
	if err != nil {
		return err
	}
	intTmpl := &x509.Certificate{
		SerialNumber: intSerial,
		Subject: pkix.Name{
			CommonName:   "LadderAirport Management Intermediate CA",
			Organization: []string{"LadderAirport"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(2, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	intDER, err := x509.CreateCertificate(rand.Reader, intTmpl, rootCert, intKey.Public(), rootKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rootKeyPath), 0o700); err != nil {
		return err
	}
	if err := writePrivateKey(rootKeyPath, rootKey); err != nil {
		return err
	}
	if err := atomicWrite(rootCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0o644); err != nil {
		return err
	}
	if err := writePrivateKey(intKeyPath, intKey); err != nil {
		return err
	}
	return atomicWrite(intCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intDER}), 0o644)
}

func parseAndValidateCSR(data []byte) (*x509.CertificateRequest, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("PEM 格式的证书请求无效")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 CSR 失败：%w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR 签名无效：%w", err)
	}
	switch key := csr.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if key.Curve != elliptic.P256() && key.Curve != elliptic.P384() {
			return nil, fmt.Errorf("不支持该 ECDSA 曲线")
		}
	default:
		return nil, fmt.Errorf("Agent CSR 必须使用 ECDSA P-256 或 P-384")
	}
	for _, name := range csr.DNSNames {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \x00") {
			return nil, fmt.Errorf("DNS SAN 无效")
		}
	}
	return csr, nil
}

func newSerial() (*big.Int, string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	buf[0] &= 0x7f
	if allZero(buf) {
		buf[len(buf)-1] = 1
	}
	return new(big.Int).SetBytes(buf), hex.EncodeToString(buf), nil
}

func loadCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("证书 PEM 无效：%s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析证书 %s 失败：%w", path, err)
	}
	return cert, nil
}

func loadSigner(path string) (crypto.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("私钥 PEM 无效：%s", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("私钥不支持签名")
	}
	return signer, nil
}

func writePrivateKey(path string, key crypto.Signer) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return atomicWrite(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func allExist(paths ...string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func anyExist(paths ...string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

func publicKeysEqual(a, b any) bool {
	aa, errA := x509.MarshalPKIXPublicKey(a)
	bb, errB := x509.MarshalPKIXPublicKey(b)
	return errA == nil && errB == nil && string(aa) == string(bb)
}
