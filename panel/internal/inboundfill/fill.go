// Package inboundfill auto-generates secrets for inbound params so operators
// only need to pick protocol, port, and a few non-secret options.
package inboundfill

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"crypto/ecdh"

	"github.com/google/uuid"
)

// Fill mutates params (or creates a map) by filling empty secret fields for protocol.
// Existing non-empty values are preserved. Returns the params map (never nil).
func Fill(protocol string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	// Normalize empty strings as missing.
	for k, v := range params {
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			delete(params, k)
		}
	}

	switch protocol {
	case "shadowsocks":
		return fillShadowsocks(params)
	case "trojan":
		return fillTrojan(params)
	case "vless":
		return fillVLESS(params)
	case "hysteria2":
		return fillHysteria2(params)
	default:
		return params, nil
	}
}

func fillShadowsocks(params map[string]any) (map[string]any, error) {
	if err := ensurePassword(params); err != nil {
		return nil, err
	}
	if empty(params, "method") {
		params["method"] = "aes-256-gcm"
	}
	if empty(params, "listen") {
		params["listen"] = "0.0.0.0"
	}
	return params, nil
}

func fillTrojan(params map[string]any) (map[string]any, error) {
	if err := ensurePassword(params); err != nil {
		return nil, err
	}
	if empty(params, "listen") {
		params["listen"] = "0.0.0.0"
	}
	if err := ensureTLSMaterial(params); err != nil {
		return nil, err
	}
	return params, nil
}

func fillHysteria2(params map[string]any) (map[string]any, error) {
	if err := ensurePassword(params); err != nil {
		return nil, err
	}
	if empty(params, "listen") {
		params["listen"] = "0.0.0.0"
	}
	if err := ensureTLSMaterial(params); err != nil {
		return nil, err
	}
	return params, nil
}

func fillVLESS(params map[string]any) (map[string]any, error) {
	if empty(params, "listen") {
		params["listen"] = "0.0.0.0"
	}
	if empty(params, "uuid") {
		params["uuid"] = uuid.NewString()
	}
	if empty(params, "tls_mode") {
		params["tls_mode"] = "reality"
	}
	mode := str(params, "tls_mode")
	switch mode {
	case "tls":
		if err := ensureTLSMaterial(params); err != nil {
			return nil, err
		}
	case "reality":
		if err := ensureReality(params); err != nil {
			return nil, err
		}
	case "none":
		// no secrets
	default:
		return nil, fmt.Errorf("invalid tls_mode %q", mode)
	}
	return params, nil
}

func ensurePassword(params map[string]any) error {
	if !empty(params, "password") {
		return nil
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("generate password: %w", err)
	}
	params["password"] = base64.RawURLEncoding.EncodeToString(b)
	return nil
}

// ensureTLSMaterial fills tls_cert_pem / tls_key_pem when neither PEM nor path is set.
func ensureTLSMaterial(params map[string]any) error {
	hasPEM := !empty(params, "tls_cert_pem") && !empty(params, "tls_key_pem")
	hasPath := !empty(params, "tls_cert_path") && !empty(params, "tls_key_path")
	if hasPEM || hasPath {
		return nil
	}
	cert, key, err := generateSelfSigned("labber-airport")
	if err != nil {
		return err
	}
	params["tls_cert_pem"] = cert
	params["tls_key_pem"] = key
	return nil
}

func ensureReality(params map[string]any) error {
	if empty(params, "private_key") {
		priv, err := generateRealityPrivateKey()
		if err != nil {
			return err
		}
		params["private_key"] = priv
	}
	if empty(params, "short_id") {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generate short_id: %w", err)
		}
		params["short_id"] = hex.EncodeToString(b)
	}
	if empty(params, "server_name") {
		params["server_name"] = "www.microsoft.com"
	}
	if empty(params, "handshake_server") {
		params["handshake_server"] = str(params, "server_name")
		if params["handshake_server"] == "" {
			params["handshake_server"] = "www.microsoft.com"
		}
	}
	if empty(params, "handshake_server_port") {
		params["handshake_server_port"] = 443
	}
	return nil
}

func generateRealityPrivateKey() (string, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("reality keypair: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.Bytes()), nil
}

func generateSelfSigned(cn string) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("tls key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"LabberAirport"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("tls cert: %w", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
	return certPEM, keyPEM, nil
}

func empty(params map[string]any, key string) bool {
	v, ok := params[key]
	if !ok || v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) == ""
	default:
		return fmt.Sprint(t) == ""
	}
}

func str(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return strings.TrimSpace(s)
}
