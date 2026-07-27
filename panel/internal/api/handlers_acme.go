package api

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	acmeservice "github.com/ladderairport/panel/internal/acme"
	"github.com/ladderairport/panel/internal/store"
)

type acmeAccountRequest struct {
	Name         string `json:"name"`
	DirectoryURL string `json:"directory_url"`
	Email        string `json:"email"`
	EABKeyID     string `json:"eab_key_id"`
	EABHMAC      string `json:"eab_hmac"`
	AcceptTerms  *bool  `json:"accept_terms"`
}

func (s *Server) handleListACMEAccounts(w http.ResponseWriter, _ *http.Request) {
	accounts, err := s.Store.ListACMEAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) handleCreateACMEAccount(w http.ResponseWriter, r *http.Request) {
	if !s.acmeReady(w) {
		return
	}
	var request acmeAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	account, err := s.buildACMEAccount(nil, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateACMEAccount(account); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (s *Server) handleUpdateACMEAccount(w http.ResponseWriter, r *http.Request) {
	if !s.acmeReady(w) {
		return
	}
	current, err := s.Store.GetACMEAccount(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var request acmeAccountRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	account, err := s.buildACMEAccount(current, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdateACMEAccount(account); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleDeleteACMEAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteACMEAccount(pathID(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegisterACMEAccount(w http.ResponseWriter, r *http.Request) {
	if !s.acmeReady(w) {
		return
	}
	account, err := s.Store.GetACMEAccount(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	uri, err := s.ACME.Register(ctx, account)
	if err != nil {
		account.Status = "error"
		account.LastError = err.Error()
		_ = s.Store.UpdateACMEAccount(account)
		writeError(w, http.StatusBadGateway, account.LastError)
		return
	}
	account.RegistrationURI = uri
	account.Status = "active"
	account.LastError = ""
	if err := s.Store.UpdateACMEAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) buildACMEAccount(current *store.ACMEAccount, request acmeAccountRequest) (*store.ACMEAccount, error) {
	account := &store.ACMEAccount{}
	if current != nil {
		*account = *current
	}
	if value := strings.TrimSpace(request.Name); value != "" {
		account.Name = value
	}
	if value := strings.TrimSpace(request.DirectoryURL); value != "" {
		if current != nil && value != current.DirectoryURL {
			account.RegistrationURI = ""
			account.Status = "pending"
		}
		account.DirectoryURL = value
	}
	account.Email = strings.TrimSpace(request.Email)
	if account.Name == "" {
		return nil, fmt.Errorf("必须提供 ACME 账号名称")
	}
	if err := validateACMEDirectoryURL(account.DirectoryURL); err != nil {
		return nil, err
	}
	if account.Email != "" {
		address, err := mail.ParseAddress(account.Email)
		if err != nil || address.Address != account.Email {
			return nil, fmt.Errorf("ACME 联系邮箱无效")
		}
	}
	if current == nil {
		account.ID = uuid.NewString()
		account.Status = "pending"
		ciphertext, err := acmeservice.EncryptNewAccountKey(s.Secrets, account.ID)
		if err != nil {
			return nil, err
		}
		account.AccountKeyCiphertext = ciphertext
	}
	eabKeyID := strings.TrimSpace(request.EABKeyID)
	if eabKeyID != "" {
		if request.EABHMAC == "" && (current == nil || eabKeyID != current.EABKeyID) {
			return nil, fmt.Errorf("设置 EAB Key ID 时必须提供 EAB HMAC")
		}
		account.EABKeyID = eabKeyID
		if request.EABHMAC != "" {
			ciphertext, err := acmeservice.EncryptEABKey(s.Secrets, account.ID, request.EABHMAC)
			if err != nil {
				return nil, err
			}
			account.EABHMACCiphertext = ciphertext
		}
	} else if request.EABHMAC != "" {
		return nil, fmt.Errorf("提供 EAB HMAC 时必须同时提供 EAB Key ID")
	} else if current == nil {
		account.EABKeyID = ""
		account.EABHMACCiphertext = ""
	}
	if request.AcceptTerms != nil {
		if *request.AcceptTerms {
			account.TermsAcceptedUnix = time.Now().Unix()
		} else {
			account.TermsAcceptedUnix = 0
		}
	}
	account.HasAccountKey = account.AccountKeyCiphertext != ""
	account.HasEABHMAC = account.EABHMACCiphertext != ""
	return account, nil
}

func validateACMEDirectoryURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("ACME 目录必须是有效的 HTTPS 地址")
	}
	return nil
}

func (s *Server) acmeReady(w http.ResponseWriter) bool {
	if s.Secrets == nil || s.ACME == nil {
		writeError(w, http.StatusServiceUnavailable, "ACME 自动化尚未初始化")
		return false
	}
	return true
}
