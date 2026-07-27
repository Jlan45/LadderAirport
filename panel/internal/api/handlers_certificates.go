package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

type protocolCertificateRequest struct {
	NodeID          string `json:"node_id"`
	ManagedDomainID string `json:"managed_domain_id"`
	ACMEAccountID   string `json:"acme_account_id"`
}

type tlsBindingRequest struct {
	Mode            string `json:"mode"`
	ManagedDomainID string `json:"managed_domain_id"`
	CertificateID   string `json:"certificate_id"`
}

func (s *Server) handleListProtocolCertificates(w http.ResponseWriter, _ *http.Request) {
	certificates, err := s.Store.ListProtocolCertificates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, certificates)
}

func (s *Server) handleCreateProtocolCertificate(w http.ResponseWriter, r *http.Request) {
	var request protocolCertificateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	domain, err := s.Store.GetManagedDomain(strings.TrimSpace(request.ManagedDomainID))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodeID := strings.TrimSpace(request.NodeID)
	if nodeID == "" {
		nodeID = domain.NodeID
	}
	if nodeID != domain.NodeID {
		writeError(w, http.StatusBadRequest, "证书节点必须与托管域名节点一致")
		return
	}
	account, err := s.Store.GetACMEAccount(strings.TrimSpace(request.ACMEAccountID))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if account.Status != "active" {
		writeError(w, http.StatusConflict, "ACME 账号尚未注册")
		return
	}
	certificate := &store.ProtocolCertificate{
		NodeID: nodeID, ManagedDomainID: domain.ID, ACMEAccountID: account.ID,
		Domains: []string{domain.FQDN}, Status: "pending",
	}
	if err := s.Store.CreateProtocolCertificate(certificate); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	job, err := s.enqueueCertificateIssue(certificate.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"certificate": certificate,
		"job":         job,
	})
}

func (s *Server) handleDeleteProtocolCertificate(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteProtocolCertificate(pathID(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIssueProtocolCertificate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.GetProtocolCertificate(pathID(r)); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	job, err := s.enqueueCertificateIssue(pathID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) enqueueCertificateIssue(id string) (*store.AutomationJob, error) {
	return s.Store.EnqueueAutomationJob(&store.AutomationJob{
		Type: "certificate.issue", TargetType: "protocol_certificate", TargetID: id,
	})
}

func (s *Server) handleGetNodeInboundTLS(w http.ResponseWriter, r *http.Request) {
	nodeID, inboundID := pathID(r), r.PathValue("inbound_id")
	exists, err := s.Store.NodeInboundPairExists(nodeID, inboundID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "节点入站关联不存在")
		return
	}
	bindings, err := s.Store.ListNodeInboundTLSBindings(nodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range bindings {
		if bindings[i].InboundID == inboundID {
			writeJSON(w, http.StatusOK, &bindings[i])
			return
		}
	}
	// Missing rows are equivalent to the backward-compatible legacy mode.
	writeJSON(w, http.StatusOK, &store.NodeInboundTLSBinding{
		NodeID: nodeID, InboundID: inboundID, Mode: "legacy",
	})
}

func (s *Server) handlePutNodeInboundTLS(w http.ResponseWriter, r *http.Request) {
	nodeID, inboundID := pathID(r), r.PathValue("inbound_id")
	exists, err := s.Store.NodeInboundPairExists(nodeID, inboundID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "节点入站关联不存在")
		return
	}
	var request tlsBindingRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "legacy"
	}
	binding := &store.NodeInboundTLSBinding{
		NodeID: nodeID, InboundID: inboundID, Mode: mode,
	}
	if mode == "managed" {
		inbound, err := s.Store.GetInbound(inboundID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !inboundSupportsManagedTLS(inbound) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("协议 %s 不支持托管 TLS", inbound.Protocol))
			return
		}
		domain, err := s.Store.GetManagedDomain(strings.TrimSpace(request.ManagedDomainID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		certificate, err := s.Store.GetProtocolCertificate(strings.TrimSpace(request.CertificateID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if domain.NodeID != nodeID || certificate.NodeID != nodeID ||
			certificate.ManagedDomainID != domain.ID {
			writeError(w, http.StatusBadRequest, "托管域名或证书不属于该节点")
			return
		}
		if certificate.Status != "active" ||
			certificate.ActiveCertPath == "" || certificate.ActiveKeyPath == "" {
			writeError(w, http.StatusConflict, "托管证书尚未就绪")
			return
		}
		binding.ManagedDomainID = domain.ID
		binding.CertificateID = certificate.ID
	} else if mode != "legacy" {
		writeError(w, http.StatusBadRequest, "TLS 绑定模式必须为 legacy 或 managed")
		return
	}
	if err := s.Store.PutNodeInboundTLSBinding(binding); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	task, message := s.deployNodeInbounds(r.Context(), nodeID)
	writeJSON(w, http.StatusOK, map[string]any{
		"binding": binding, "apply_task": task, "message": message,
	})
}

func inboundSupportsManagedTLS(inbound *store.InboundConfig) bool {
	switch inbound.Protocol {
	case "trojan", "hysteria2", "tuic", "anytls":
		return true
	case "vless", "vmess":
		mode, _ := inbound.Params["tls_mode"].(string)
		return mode != "reality"
	default:
		return false
	}
}
