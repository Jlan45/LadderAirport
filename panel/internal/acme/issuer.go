package acme

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	xacme "golang.org/x/crypto/acme"
)

type ProtocolClient interface {
	AuthorizeOrder(ctx context.Context, id []xacme.AuthzID, opt ...xacme.OrderOption) (*xacme.Order, error)
	GetAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	DNS01ChallengeRecord(token string) (string, error)
	Accept(ctx context.Context, challenge *xacme.Challenge) (*xacme.Challenge, error)
	WaitAuthorization(ctx context.Context, url string) (*xacme.Authorization, error)
	WaitOrder(ctx context.Context, url string) (*xacme.Order, error)
	CreateOrderCert(ctx context.Context, finalizeURL string, csr []byte, bundle bool) ([][]byte, string, error)
}

type DNS01Presenter interface {
	Present(ctx context.Context, fqdn, value string) error
	Wait(ctx context.Context, fqdn, value string) error
	Cleanup(ctx context.Context, fqdn, value string) error
}

type IssuanceResult struct {
	FullChainPEM   string
	OrderURL       string
	CertificateURL string
}

func Issue(
	ctx context.Context,
	client ProtocolClient,
	presenter DNS01Presenter,
	csrPEM string,
	domains []string,
) (*IssuanceResult, error) {
	csrDER, err := parseAndValidateCSR(csrPEM, domains)
	if err != nil {
		return nil, err
	}
	order, err := client.AuthorizeOrder(ctx, xacme.DomainIDs(domains...))
	if err != nil {
		return nil, fmt.Errorf("创建 ACME 订单失败：%w", err)
	}
	for _, authorizationURL := range order.AuthzURLs {
		authorization, err := client.GetAuthorization(ctx, authorizationURL)
		if err != nil {
			return nil, fmt.Errorf("读取 ACME 授权失败：%w", err)
		}
		if authorization.Status == xacme.StatusValid {
			continue
		}
		challenge := dns01Challenge(authorization)
		if challenge == nil {
			return nil, fmt.Errorf("域名 %s 没有 DNS-01 挑战", authorization.Identifier.Value)
		}
		value, err := client.DNS01ChallengeRecord(challenge.Token)
		if err != nil {
			return nil, fmt.Errorf("计算 DNS-01 验证值失败：%w", err)
		}
		fqdn := "_acme-challenge." + strings.TrimPrefix(authorization.Identifier.Value, "*.")
		if err := presentAndAuthorize(ctx, client, presenter, authorizationURL, challenge, fqdn, value); err != nil {
			return nil, err
		}
	}
	ready, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, fmt.Errorf("等待 ACME 订单就绪失败：%w", err)
	}
	chain, certificateURL, err := client.CreateOrderCert(ctx, ready.FinalizeURL, csrDER, true)
	if err != nil {
		return nil, fmt.Errorf("完成 ACME 订单失败：%w", err)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("ACME 服务返回了空证书链")
	}
	var fullChain strings.Builder
	for _, der := range chain {
		if _, err := x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("ACME 服务返回的证书无效：%w", err)
		}
		_ = pem.Encode(&fullChain, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	return &IssuanceResult{
		FullChainPEM: fullChain.String(), OrderURL: order.URI,
		CertificateURL: certificateURL,
	}, nil
}

func presentAndAuthorize(
	ctx context.Context,
	client ProtocolClient,
	presenter DNS01Presenter,
	authorizationURL string,
	challenge *xacme.Challenge,
	fqdn, value string,
) (err error) {
	if err := presenter.Present(ctx, fqdn, value); err != nil {
		return fmt.Errorf("写入 DNS-01 记录失败：%w", err)
	}
	defer func() {
		if cleanupErr := presenter.Cleanup(context.WithoutCancel(ctx), fqdn, value); err == nil && cleanupErr != nil {
			err = fmt.Errorf("清理 DNS-01 记录失败：%w", cleanupErr)
		}
	}()
	if err := presenter.Wait(ctx, fqdn, value); err != nil {
		return fmt.Errorf("等待 DNS-01 记录生效失败：%w", err)
	}
	if _, err := client.Accept(ctx, challenge); err != nil {
		return fmt.Errorf("提交 DNS-01 挑战失败：%w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authorizationURL); err != nil {
		return fmt.Errorf("等待 DNS-01 授权失败：%w", err)
	}
	return nil
}

func dns01Challenge(authorization *xacme.Authorization) *xacme.Challenge {
	for _, challenge := range authorization.Challenges {
		if challenge.Type == "dns-01" {
			return challenge
		}
	}
	return nil
}

func parseAndValidateCSR(value string, domains []string) ([]byte, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("Agent CSR 格式无效")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 Agent CSR 失败：%w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("Agent CSR 签名无效：%w", err)
	}
	if !sameNames(csr.DNSNames, domains) {
		return nil, fmt.Errorf("Agent CSR 域名与申请域名不一致")
	}
	return block.Bytes, nil
}

func sameNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, value := range left {
		counts[strings.ToLower(strings.TrimSuffix(value, "."))]++
	}
	for _, value := range right {
		key := strings.ToLower(strings.TrimSuffix(value, "."))
		if counts[key] == 0 {
			return false
		}
		counts[key]--
	}
	return true
}
