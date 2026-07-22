package api

import (
	"net/http"

	"github.com/ladderairport/panel/internal/inboundfill"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/internal/templates"
)

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, templates.List())
}

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListInbounds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createInboundBody struct {
	Name     string         `json:"name"`
	Protocol string         `json:"protocol"`
	Params   map[string]any `json:"params"`
	Enabled  *bool          `json:"enabled"`
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var body createInboundBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" || body.Protocol == "" {
		writeError(w, http.StatusBadRequest, "name and protocol required")
		return
	}
	if _, ok := templates.Get(body.Protocol); !ok {
		writeError(w, http.StatusBadRequest, "unknown protocol")
		return
	}
	// Auto-generate passwords, UUIDs, TLS PEMs, Reality keys when omitted.
	filled, err := inboundfill.Fill(body.Protocol, body.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	in := store.InboundConfig{
		Name:     body.Name,
		Protocol: body.Protocol,
		Params:   filled,
		Enabled:  enabled,
	}
	if err := s.Store.CreateInbound(&in); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.Store.GetInbound(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body struct {
		Name     *string        `json:"name"`
		Protocol *string        `json:"protocol"`
		Params   map[string]any `json:"params"`
		Enabled  *bool          `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name != nil && *body.Name != "" {
		existing.Name = *body.Name
	}
	if body.Protocol != nil && *body.Protocol != "" {
		existing.Protocol = *body.Protocol
	}
	if _, ok := templates.Get(existing.Protocol); !ok {
		writeError(w, http.StatusBadRequest, "unknown protocol")
		return
	}
	if body.Params != nil {
		// Merge: keep existing secrets if client omitted them, then fill remaining gaps.
		merged := map[string]any{}
		for k, v := range existing.Params {
			merged[k] = v
		}
		for k, v := range body.Params {
			merged[k] = v
		}
		filled, err := inboundfill.Fill(existing.Protocol, merged)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.Params = filled
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if err := s.Store.UpdateInbound(existing); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetInbound(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.Store.DeleteInbound(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
