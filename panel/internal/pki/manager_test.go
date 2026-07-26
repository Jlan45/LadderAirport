package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenBootstrapAndSignAgentCSR(t *testing.T) {
	dir := t.TempDir()
	manager, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, name := range []string{
		"root-ca.crt",
		"intermediate-ca.crt",
		"intermediate-ca.key",
		"panel-client.crt",
		"panel-client.key",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	keyInfo, err := os.Stat(filepath.Join(dir, "intermediate-ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("intermediate key mode = %o, want 600", keyInfo.Mode().Perm())
	}

	key, csr := makeCSR(t, []string{"agent.example.test"}, []net.IP{net.ParseIP("192.0.2.4")})
	issued, err := manager.SignAgentCSR("node-123", csr, time.Now())
	if err != nil {
		t.Fatalf("SignAgentCSR: %v", err)
	}
	if issued.Certificate.PublicKey.(*ecdsa.PublicKey).X.Cmp(key.PublicKey.X) != 0 {
		t.Fatal("issued certificate does not contain CSR public key")
	}
	if got := issued.Certificate.URIs[0].String(); got != AgentURI("node-123") {
		t.Fatalf("URI SAN = %q", got)
	}
	if issued.Certificate.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("unexpected EKU: %v", issued.Certificate.ExtKeyUsage)
	}
	roots := x509.NewCertPool()
	roots.AddCert(manager.root)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(manager.intermediate)
	if _, err := issued.Certificate.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       "agent.example.test",
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify issued certificate: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Status().RootSubject != manager.Status().RootSubject {
		t.Fatal("reopen changed CA identity")
	}
	oldIntermediateSerial := reopened.intermediate.SerialNumber.String()
	if err := reopened.RotateIntermediate(time.Now()); err != nil {
		t.Fatalf("RotateIntermediate: %v", err)
	}
	if reopened.intermediate.SerialNumber.String() == oldIntermediateSerial {
		t.Fatal("intermediate serial did not change")
	}
	if err := reopened.intermediate.CheckSignatureFrom(reopened.root); err != nil {
		t.Fatalf("rotated intermediate signature: %v", err)
	}
	if reopened.clientCert.Leaf == nil || reopened.clientCert.Leaf.URIs[0].String() != PanelURI() {
		t.Fatal("Panel client identity not rotated")
	}
}

func TestSignAgentCSRRejectsMissingSAN(t *testing.T) {
	manager, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, csr := makeCSR(t, nil, nil)
	if _, err := manager.SignAgentCSR("node-123", csr, time.Now()); err == nil {
		t.Fatal("expected missing SAN to be rejected")
	}
}

func makeCSR(t *testing.T, dns []string, ips []net.IP) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: "untrusted-client-cn"},
		DNSNames:    dns,
		IPAddresses: ips,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}
