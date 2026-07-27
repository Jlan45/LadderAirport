package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"
)

type fakeProtocolClient struct {
	certificateDER []byte
}

func (f *fakeProtocolClient) AuthorizeOrder(context.Context, []xacme.AuthzID, ...xacme.OrderOption) (*xacme.Order, error) {
	return &xacme.Order{
		URI: "https://ca.test/order/1", AuthzURLs: []string{"https://ca.test/authz/1"},
		FinalizeURL: "https://ca.test/finalize/1",
	}, nil
}
func (f *fakeProtocolClient) GetAuthorization(context.Context, string) (*xacme.Authorization, error) {
	return &xacme.Authorization{
		Identifier: xacme.AuthzID{Type: "dns", Value: "edge.example.com"},
		Status:     xacme.StatusPending,
		Challenges: []*xacme.Challenge{{Type: "dns-01", Token: "token"}},
	}, nil
}
func (f *fakeProtocolClient) DNS01ChallengeRecord(string) (string, error) {
	return "txt-value", nil
}
func (f *fakeProtocolClient) Accept(context.Context, *xacme.Challenge) (*xacme.Challenge, error) {
	return &xacme.Challenge{Status: xacme.StatusPending}, nil
}
func (f *fakeProtocolClient) WaitAuthorization(context.Context, string) (*xacme.Authorization, error) {
	return &xacme.Authorization{Status: xacme.StatusValid}, nil
}
func (f *fakeProtocolClient) WaitOrder(context.Context, string) (*xacme.Order, error) {
	return &xacme.Order{FinalizeURL: "https://ca.test/finalize/1", Status: xacme.StatusReady}, nil
}
func (f *fakeProtocolClient) CreateOrderCert(context.Context, string, []byte, bool) ([][]byte, string, error) {
	return [][]byte{f.certificateDER}, "https://ca.test/cert/1", nil
}

type fakePresenter struct {
	presented []string
	cleaned   []string
	waitErr   error
}

func (f *fakePresenter) Present(_ context.Context, fqdn, value string) error {
	f.presented = append(f.presented, fqdn+"="+value)
	return nil
}
func (f *fakePresenter) Wait(context.Context, string, string) error { return f.waitErr }
func (f *fakePresenter) Cleanup(_ context.Context, fqdn, value string) error {
	f.cleaned = append(f.cleaned, fqdn+"="+value)
	return nil
}

func TestIssueUsesAgentCSRAndCleansTXT(t *testing.T) {
	csrPEM, key := makeCSR(t, []string{"edge.example.com"})
	certDER := makeCertificate(t, key, []string{"edge.example.com"})
	client := &fakeProtocolClient{certificateDER: certDER}
	presenter := &fakePresenter{}
	result, err := Issue(context.Background(), client, presenter, csrPEM, []string{"edge.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if result.FullChainPEM == "" || result.OrderURL == "" ||
		len(presenter.presented) != 1 || len(presenter.cleaned) != 1 {
		t.Fatalf("result=%+v presenter=%+v", result, presenter)
	}
	if presenter.presented[0] != "_acme-challenge.edge.example.com=txt-value" {
		t.Fatalf("presented = %v", presenter.presented)
	}
}

func TestIssueCleansTXTWhenPropagationFails(t *testing.T) {
	csrPEM, key := makeCSR(t, []string{"edge.example.com"})
	client := &fakeProtocolClient{certificateDER: makeCertificate(t, key, []string{"edge.example.com"})}
	presenter := &fakePresenter{waitErr: errors.New("not propagated")}
	if _, err := Issue(context.Background(), client, presenter, csrPEM, []string{"edge.example.com"}); err == nil {
		t.Fatal("propagation failure ignored")
	}
	if len(presenter.cleaned) != 1 {
		t.Fatalf("cleanup not called: %+v", presenter)
	}
}

func TestIssueRejectsCSRDomainMismatch(t *testing.T) {
	csrPEM, _ := makeCSR(t, []string{"other.example.com"})
	if _, err := Issue(context.Background(), &fakeProtocolClient{}, &fakePresenter{}, csrPEM, []string{"edge.example.com"}); err == nil {
		t.Fatal("mismatched CSR accepted")
	}
}

func makeCSR(t *testing.T, domains []string) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), key
}

func makeCertificate(t *testing.T, key *ecdsa.PrivateKey, domains []string) []byte {
	t.Helper()
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domains[0]},
		DNSNames: domains, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
