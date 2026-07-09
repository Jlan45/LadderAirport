package api

import (
	"net/http"

	"github.com/labberairport/panel/internal/store"
	"github.com/labberairport/panel/internal/templates"
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

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var in store.InboundConfig
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.Name == "" || in.Protocol == "" {
		writeError(w, http.StatusBadRequest, "name and protocol required")
		return
	}
	// Default enabled=true when omitted (zero value is false; clients usually send it).
	// Keep explicit false if client sets Enabled and sends the field — zero value stays false
	// which is fine for create-as-disabled.
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
	var body store.InboundConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.ID = id
	// Preserve created timestamp.
	body.CreatedAtUnix = existing.CreatedAtUnix
	if body.Name == "" {
		body.Name = existing.Name
	}
	if body.Protocol == "" {
		body.Protocol = existing.Protocol
	}
	if body.Params == nil {
		body.Params = existing.Params
	}
	if err := s.Store.UpdateInbound(&body); err != nil {
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
