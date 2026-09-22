// Package panelhttp is the shared HTTP client for Agent → Panel calls
// (PKI renew, uplink report, config HEAD/POST). One Transport keeps the TLS
// connection alive so periodic polls do not redo a full handshake.
package panelhttp

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	RequestTimeout  = 30 * time.Second
	IdleConnTimeout = 3 * time.Minute
)

// NewClient returns an HTTP client with keep-alive and TLS session resumption.
func NewClient() *http.Client {
	return &http.Client{
		Timeout:   RequestTimeout,
		Transport: NewTransport(),
	}
}

// NewTransport is a dedicated pool for the Panel host. Callers that need to
// inject test TLS settings should Clone() this transport first.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       4,
		IdleConnTimeout:       IdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ClientSessionCache: tls.NewLRUClientSessionCache(32),
		},
	}
}
