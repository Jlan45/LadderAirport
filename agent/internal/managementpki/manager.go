// Package managementpki manages the Agent's local private key, Panel-issued
// server certificate and automatic renewal.
package managementpki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ladderairport/agent/internal/fileutil"
)

type Config struct {
	PanelURL   string
	NodeID     string
	Token      string
	CertPath   string
	KeyPath    string
	CAPath     string
	Address    string
	GRPCPort   int
	SANs       []string
	HTTPClient *http.Client
}

type Manager struct {
	cfg  Config
	mu   sync.RWMutex
	cert *tls.Certificate
}

type issueResponse struct {
	CertPEM     string `json:"cert_pem"`
	CABundlePEM string `json:"ca_bundle_pem"`
}

func New(cfg Config) (*Manager, error) {
	if cfg.CertPath == "" || cfg.KeyPath == "" || cfg.CAPath == "" ||
		strings.TrimSpace(cfg.PanelURL) == "" || strings.TrimSpace(cfg.NodeID) == "" ||
		strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("必须提供 Panel URL、节点 ID、令牌、证书、私钥和 CA 路径")
	}
	if err := ParsePanelURL(cfg.PanelURL); err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg}
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil {
		return nil, fmt.Errorf("管理证书不可用")
	}
	return m.cert, nil
}

func (m *Manager) Leaf() *x509.Certificate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return nil
	}
	return m.cert.Leaf
}

func (m *Manager) Run(ctx context.Context) {
	if strings.TrimSpace(m.cfg.PanelURL) == "" || strings.TrimSpace(m.cfg.NodeID) == "" {
		log.Printf("管理 PKI 续签已禁用：缺少 Panel URL 或节点 ID")
		return
	}
	m.renewIfNeeded(ctx)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.renewIfNeeded(ctx)
		}
	}
}

func (m *Manager) renewIfNeeded(ctx context.Context) {
	leaf := m.Leaf()
	if leaf != nil {
		lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
		if time.Now().Before(leaf.NotBefore.Add(lifetime * 2 / 3)) {
			return
		}
	}
	renewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := m.Renew(renewCtx); err != nil {
		log.Printf("管理证书续签失败：%v", err)
		return
	}
	log.Printf("管理证书已续签，到期时间=%s", m.Leaf().NotAfter.Format(time.RFC3339))
}

func (m *Manager) Renew(ctx context.Context) error {
	csrPEM, err := m.createCSR()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"node_id":   m.cfg.NodeID,
		"csr_pem":   string(csrPEM),
		"address":   m.cfg.Address,
		"grpc_port": m.cfg.GRPCPort,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(m.cfg.PanelURL, "/") + "/api/v1/pki/agent-certificates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	client := m.cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("向 Panel 申请证书失败：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("Panel 证书接口返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var issued issueResponse
	if err := json.Unmarshal(raw, &issued); err != nil {
		return fmt.Errorf("解析 Panel 证书响应失败：%w", err)
	}
	if issued.CertPEM == "" || issued.CABundlePEM == "" {
		return fmt.Errorf("Panel 返回的证书材料不完整")
	}
	keyPEM, err := os.ReadFile(m.cfg.KeyPath)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair([]byte(issued.CertPEM), keyPEM)
	if err != nil {
		return fmt.Errorf("校验签发证书失败：%w", err)
	}
	if err := validateAgentCertificate(&pair, []byte(issued.CABundlePEM), m.cfg.NodeID, time.Now()); err != nil {
		return fmt.Errorf("校验签发证书失败：%w", err)
	}
	if err := fileutil.AtomicWrite(m.cfg.CertPath, []byte(issued.CertPEM), 0o640); err != nil {
		return err
	}
	if m.cfg.CAPath != "" {
		if err := fileutil.AtomicWrite(m.cfg.CAPath, []byte(issued.CABundlePEM), 0o644); err != nil {
			return err
		}
	}
	return m.reload()
}

func (m *Manager) reload() error {
	pair, err := tls.LoadX509KeyPair(m.cfg.CertPath, m.cfg.KeyPath)
	if err != nil {
		return fmt.Errorf("加载管理证书失败：%w", err)
	}
	if len(pair.Certificate) == 0 {
		return fmt.Errorf("管理证书链为空")
	}
	caPEM, err := os.ReadFile(m.cfg.CAPath)
	if err != nil {
		return err
	}
	if err := validateAgentCertificate(&pair, caPEM, m.cfg.NodeID, time.Now()); err != nil {
		return fmt.Errorf("校验管理证书失败：%w", err)
	}
	m.mu.Lock()
	m.cert = &pair
	m.mu.Unlock()
	return nil
}

func validateAgentCertificate(pair *tls.Certificate, caPEM []byte, nodeID string, now time.Time) error {
	if pair == nil || len(pair.Certificate) == 0 {
		return fmt.Errorf("管理证书链为空")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("管理 CA 证书包无效")
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		cert, parseErr := x509.ParseCertificate(raw)
		if parseErr != nil {
			return parseErr
		}
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("验证管理证书链失败：%w", err)
	}
	want := "spiffe://ladderairport/agent/" + nodeID
	for _, uri := range leaf.URIs {
		if uri.String() == want {
			pair.Leaf = leaf
			return nil
		}
	}
	return fmt.Errorf("Agent 证书身份不符合预期")
}

func (m *Manager) createCSR() ([]byte, error) {
	key, err := loadECDSAKey(m.cfg.KeyPath)
	if err != nil {
		return nil, err
	}
	dns, ips := m.currentSANs()
	req := &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: m.cfg.NodeID},
		DNSNames:    dns,
		IPAddresses: ips,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, req, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

func (m *Manager) currentSANs() ([]string, []net.IP) {
	dns := []string{}
	ips := []net.IP{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if zone := strings.LastIndex(value, "%"); zone > 0 && strings.Contains(value[:zone], ":") {
			value = value[:zone]
		}
		if value == "" || value == "0.0.0.0" || value == "::" || seen[value] {
			return
		}
		seen[value] = true
		if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, value)
		}
	}
	if leaf := m.Leaf(); leaf != nil {
		for _, name := range leaf.DNSNames {
			add(name)
		}
		for _, ip := range leaf.IPAddresses {
			add(ip.String())
		}
	}
	for _, san := range m.cfg.SANs {
		add(strings.TrimPrefix(strings.TrimPrefix(san, "DNS:"), "IP:"))
	}
	add(m.cfg.Address)
	if host, err := os.Hostname(); err == nil {
		add(host)
	}
	if len(dns)+len(ips) == 0 {
		ips = append(ips, net.ParseIP("127.0.0.1"))
	}
	return dns, ips
}

func loadECDSAKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("私钥 PEM 无效")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ec, ok := key.(*ecdsa.PrivateKey); ok {
			return ec, nil
		}
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("管理私钥必须使用 ECDSA")
}

func ClientCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("客户端 CA 证书包无效")
	}
	return pool, nil
}

// ClientCAPoolCache caches the parsed client CA pool and rebuilds it only when
// the CA file's mtime changes (certificate renewal rewrites the file). It is
// safe for concurrent use from TLS handshakes.
type ClientCAPoolCache struct {
	path  string
	mu    sync.Mutex
	pool  *x509.CertPool
	mtime time.Time
}

func NewClientCAPoolCache(path string) *ClientCAPoolCache {
	return &ClientCAPoolCache{path: path}
}

// Pool returns the cached CA pool, reloading from disk when the file changed.
func (c *ClientCAPoolCache) Pool() (*x509.CertPool, error) {
	info, err := os.Stat(c.path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pool != nil && info.ModTime().Equal(c.mtime) {
		return c.pool, nil
	}
	pool, err := ClientCAPool(c.path)
	if err != nil {
		return nil, err
	}
	c.pool = pool
	c.mtime = info.ModTime()
	return pool, nil
}

func VerifyPanelIdentity(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("必须提供 Panel 客户端证书")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	const want = "spiffe://ladderairport/panel/control"
	for _, uri := range leaf.URIs {
		if uri.String() == want {
			return nil
		}
	}
	return fmt.Errorf("管理客户端身份不符合预期")
}

func ParsePanelURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("Panel URL 无效")
	}
	return nil
}
