package api

import (
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

type issueAgentCertificateRequest struct {
	NodeID   string `json:"node_id"`
	Token    string `json:"token,omitempty"`
	CSRPEM   string `json:"csr_pem"`
	Address  string `json:"address,omitempty"`
	GRPCPort int    `json:"grpc_port,omitempty"`
}

type issueAgentCertificateResponse struct {
	Serial       string `json:"serial"`
	CertPEM      string `json:"cert_pem"`
	CABundlePEM  string `json:"ca_bundle_pem"`
	NotBefore    int64  `json:"not_before_unix"`
	NotAfter     int64  `json:"not_after_unix"`
	RenewAfter   int64  `json:"renew_after_unix"`
	ManagementID string `json:"management_identity"`
	ControlToken string `json:"control_token,omitempty"`
}

func (s *Server) handleIssueAgentCertificate(w http.ResponseWriter, r *http.Request) {
	if s.PKI == nil {
		writeError(w, http.StatusServiceUnavailable, "management PKI disabled")
		return
	}
	var req issueAgentCertificateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	n, err := s.Store.GetNode(req.NodeID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "node not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = bearerToken(r)
	}
	want := strings.TrimSpace(n.Token)
	nodeTokenValid := want != "" && token != "" && subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
	enrollmentTokenValid := false
	if !nodeTokenValid && (n.PKICertSerial == "" || n.PKIMigrationRequired) && token != "" {
		enrollmentTokenValid, err = s.Store.ConsumePKIEnrollmentToken(n.ID, token)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if !nodeTokenValid && !enrollmentTokenValid {
		writeError(w, http.StatusUnauthorized, "invalid or expired token for node")
		return
	}
	if enrollmentTokenValid && want == "" {
		want, err = randomAgentToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "generate control token: "+err.Error())
			return
		}
		n.Token = want
		if err := s.Store.UpdateNode(n); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	issued, err := s.PKI.SignAgentCSR(n.ID, []byte(req.CSRPEM), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cert := issued.Certificate
	dns, ips := certificateSANs(cert)
	record := &store.PKICertificate{
		Serial:        issued.Serial,
		NodeID:        n.ID,
		Profile:       "agent-server",
		Subject:       cert.Subject.String(),
		URISAN:        firstURI(cert),
		DNSSANs:       strings.Join(dns, ","),
		IPSANs:        strings.Join(ips, ","),
		NotBeforeUnix: cert.NotBefore.Unix(),
		NotAfterUnix:  cert.NotAfter.Unix(),
		Status:        "active",
		CertPEM:       string(issued.CertPEM),
		CreatedAtUnix: time.Now().Unix(),
	}
	if err := s.Store.ReplaceActivePKICertificate(record, string(issued.CABundlePEM)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Enrollment is also the authoritative place to fill missing reachability
	// data. Existing operator-provided NAT/public overrides are preserved.
	if strings.TrimSpace(req.Address) != "" || req.GRPCPort > 0 {
		updated, getErr := s.Store.GetNode(n.ID)
		if getErr == nil {
			if updated.Address == "" {
				updated.Address = strings.TrimSpace(req.Address)
			}
			if updated.GRPCPort == 0 && req.GRPCPort > 0 && req.GRPCPort <= 65535 {
				updated.GRPCPort = req.GRPCPort
			}
			if updated.Status == "" || updated.Status == "pending" || updated.Status == "unauthorized" {
				updated.Status = "unknown"
				updated.LastError = ""
			}
			_ = s.Store.UpdateNode(updated)
		}
	}
	action := "agent.certificate.issue"
	if n.PKICertSerial != "" {
		action = "agent.certificate.renew"
	}
	_ = s.Store.AddPKIAudit(action, n.ID, issued.Serial, "node:"+n.ID, "")
	writeJSON(w, http.StatusCreated, issueAgentCertificateResponse{
		Serial:       issued.Serial,
		CertPEM:      string(issued.CertPEM),
		CABundlePEM:  string(issued.CABundlePEM),
		NotBefore:    cert.NotBefore.Unix(),
		NotAfter:     cert.NotAfter.Unix(),
		RenewAfter:   cert.NotBefore.Add(cert.NotAfter.Sub(cert.NotBefore) * 2 / 3).Unix(),
		ManagementID: firstURI(cert),
		ControlToken: want,
	})
}

func (s *Server) handleCompleteAgentPKIMigration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"node_id"`
		Serial string `json:"serial"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	req.Serial = strings.TrimSpace(req.Serial)
	n, err := s.Store.GetNode(req.NodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	token := bearerToken(r)
	if n.Token == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(n.Token), []byte(token)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid token for node")
		return
	}
	if err := s.Store.CompletePKIMigration(n.ID, req.Serial); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.Store.AddPKIAudit("agent.migration.complete", n.ID, req.Serial, "node:"+n.ID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePKIBundle(w http.ResponseWriter, _ *http.Request) {
	if s.PKI == nil {
		writeError(w, http.StatusServiceUnavailable, "management PKI disabled")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.PKI.BundlePEM())
}

func (s *Server) handlePKIStatus(w http.ResponseWriter, _ *http.Request) {
	if s.PKI == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	status := s.PKI.Status()
	certs, _ := s.Store.ListPKICertificates()
	active := 0
	expiring := 0
	now := time.Now()
	for _, cert := range certs {
		if cert.Status == "active" {
			active++
			if time.Unix(cert.NotAfterUnix, 0).Before(now.Add(7 * 24 * time.Hour)) {
				expiring++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":                     status.Enabled,
		"root_subject":                status.RootSubject,
		"root_not_after_unix":         status.RootNotAfterUnix,
		"intermediate_subject":        status.IntermediateSubject,
		"intermediate_not_after_unix": status.IntermediateNotAfterUnix,
		"agent_lifetime_seconds":      status.AgentLifetimeSeconds,
		"directory":                   status.Directory,
		"root_key_online":             status.RootKeyOnline,
		"active_certificates":         active,
		"expiring_certificates":       expiring,
	})
}

func (s *Server) handleListPKICertificates(w http.ResponseWriter, _ *http.Request) {
	list, err := s.Store.ListPKICertificates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRevokePKICertificate(w http.ResponseWriter, r *http.Request) {
	serial := strings.TrimSpace(r.PathValue("serial"))
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial required")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	cert, err := s.Store.GetPKICertificate(serial)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.Store.RevokePKICertificate(serial, strings.TrimSpace(body.Reason)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.Store.AddPKIAudit("certificate.revoke", cert.NodeID, serial, "admin", body.Reason)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "serial": serial})
}

func (s *Server) handleListPKIAuditLogs(w http.ResponseWriter, _ *http.Request) {
	list, err := s.Store.ListPKIAuditLogs(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authz, prefix))
}

// Concrete helpers remain separate to keep response shaping easy to audit.
func firstURI(cert *x509.Certificate) string {
	if len(cert.URIs) == 0 {
		return ""
	}
	return cert.URIs[0].String()
}

func certificateSANs(cert *x509.Certificate) ([]string, []string) {
	dns := slices.Clone(cert.DNSNames)
	ips := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	return dns, ips
}
