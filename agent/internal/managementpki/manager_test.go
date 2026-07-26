package managementpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyPanelIdentity(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	panelURI, _ := url.Parse("spiffe://ladderairport/panel/control")
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "panel"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		URIs:                  []*url.URL{panelURI},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPanelIdentity([][]byte{der}, nil); err != nil {
		t.Fatalf("VerifyPanelIdentity: %v", err)
	}
	template.URIs = nil
	template.SerialNumber = big.NewInt(2)
	der, _ = x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err := VerifyPanelIdentity([][]byte{der}, nil); err == nil {
		t.Fatal("expected wrong identity to be rejected")
	}
}

func TestLoadECDSAKeyPKCS8(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadECDSAKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.X.Cmp(key.X) != 0 {
		t.Fatal("loaded different key")
	}
}

func TestValidateAgentCertificateChainAndIdentity(t *testing.T) {
	now := time.Now()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(10),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	agentKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	agentURI, _ := url.Parse("spiffe://ladderairport/agent/node-1")
	agentTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(11),
		Subject:               pkix.Name{CommonName: "node-1"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		URIs:                  []*url.URL{agentURI},
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	agentDER, err := x509.CreateCertificate(rand.Reader, agentTemplate, caCert, agentKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	pair := &tls.Certificate{Certificate: [][]byte{agentDER}}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := validateAgentCertificate(pair, caPEM, "node-1", now); err != nil {
		t.Fatalf("validateAgentCertificate: %v", err)
	}
	if pair.Leaf == nil {
		t.Fatal("validated certificate did not cache leaf")
	}
	if err := validateAgentCertificate(pair, caPEM, "node-2", now); err == nil {
		t.Fatal("expected wrong node identity to be rejected")
	}
}
