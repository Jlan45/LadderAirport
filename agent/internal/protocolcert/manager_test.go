package protocolcert

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func issueFromCSR(t *testing.T, csrPEM string, dnsNames []string, notAfter time.Time) string {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		t.Fatal("CSR PEM missing")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	caKey, err := loadOrCreateKey(filepath.Join(t.TempDir(), "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: csr.Subject,
		DNSNames: dnsNames, NotBefore: now.Add(-time.Minute), NotAfter: notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
}

func TestPrepareInstallStatusAndDelete(t *testing.T) {
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.Prepare("cert_1", "gen_1", []string{"edge.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.KeyID != "cert_1/gen_1" || prepared.CSRPEM == "" ||
		prepared.PublicKeyFingerprint == "" {
		t.Fatalf("prepared = %+v", prepared)
	}
	again, err := manager.Prepare("cert_1", "gen_1", []string{"edge.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if again.PublicKeyFingerprint != prepared.PublicKeyFingerprint {
		t.Fatal("idempotent prepare changed private key")
	}
	chain := issueFromCSR(t, prepared.CSRPEM, []string{"edge.example.com"}, time.Now().Add(time.Hour))
	installed, err := manager.Install(
		"cert_1", "gen_1", prepared.KeyID, chain,
		[]string{"edge.example.com"}, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if installed.CertificatePath == "" || installed.KeyPath == "" || installed.Fingerprint == "" {
		t.Fatalf("installed = %+v", installed)
	}
	info, err := os.Stat(installed.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	status, err := manager.Status("cert_1", "gen_1")
	if err != nil {
		t.Fatal(err)
	}
	if !status.KeyExists || !status.CertificateExists || status.Fingerprint == "" {
		t.Fatalf("status = %+v", status)
	}
	if err := manager.Delete("cert_1", "gen_1"); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status("cert_1", "gen_1")
	if err != nil {
		t.Fatal(err)
	}
	if status.KeyExists || status.CertificateExists {
		t.Fatalf("status after delete = %+v", status)
	}
}

func TestInstallRejectsSANMismatchAndTraversal(t *testing.T) {
	manager, _ := New(t.TempDir())
	prepared, err := manager.Prepare("cert", "gen", []string{"edge.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	chain := issueFromCSR(t, prepared.CSRPEM, []string{"other.example.com"}, time.Now().Add(time.Hour))
	if _, err := manager.Install(
		"cert", "gen", prepared.KeyID, chain,
		[]string{"edge.example.com"}, time.Now(),
	); err == nil {
		t.Fatal("accepted SAN mismatch")
	}
	if _, err := manager.Prepare("../escape", "gen", []string{"edge.example.com"}); err == nil {
		t.Fatal("accepted path traversal")
	}
}
