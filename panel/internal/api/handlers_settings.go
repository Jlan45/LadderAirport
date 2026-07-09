package api

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type putSettingsBody struct {
	DefaultAgentToken *string `json:"default_agent_token"`
	GRPCTimeoutSec    *int    `json:"grpc_timeout_sec"`
	MaxConcurrency    *int    `json:"max_concurrency"`
	ListenAddr        *string `json:"listen_addr"`
	PublicBaseURL     *string `json:"public_base_url"`
	NewPassword       *string `json:"new_password"`
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body putSettingsBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.DefaultAgentToken != nil {
		st.DefaultAgentToken = *body.DefaultAgentToken
	}
	if body.GRPCTimeoutSec != nil {
		if *body.GRPCTimeoutSec <= 0 {
			writeError(w, http.StatusBadRequest, "grpc_timeout_sec must be positive")
			return
		}
		st.GRPCTimeoutSec = *body.GRPCTimeoutSec
	}
	if body.MaxConcurrency != nil {
		if *body.MaxConcurrency <= 0 {
			writeError(w, http.StatusBadRequest, "max_concurrency must be positive")
			return
		}
		st.MaxConcurrency = *body.MaxConcurrency
	}
	if body.ListenAddr != nil {
		st.ListenAddr = *body.ListenAddr
	}
	if body.PublicBaseURL != nil {
		st.PublicBaseURL = strings.TrimSpace(*body.PublicBaseURL)
	}
	if body.NewPassword != nil {
		if *body.NewPassword == "" {
			writeError(w, http.StatusBadRequest, "new_password must not be empty")
			return
		}
		hash, err := HashPassword(*body.NewPassword)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		st.AdminPasswordHash = hash
	}
	if err := s.Store.SaveSettings(st); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Reflect timeout/concurrency on runner when present.
	if s.Runner != nil {
		if body.GRPCTimeoutSec != nil {
			s.Runner.Timeout = time.Duration(*body.GRPCTimeoutSec) * time.Second
		}
		if body.MaxConcurrency != nil {
			s.Runner.MaxConcurrency = *body.MaxConcurrency
		}
	}
	out, err := s.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
