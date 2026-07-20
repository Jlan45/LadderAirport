package api

import (
	"net/http"
	"strings"

	"github.com/ladderairport/panel/internal/store"
)

func (s *Server) handleListExternalSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListExternalSources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createExternalSourceBody struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers"`
	Enabled            *bool             `json:"enabled"`
	RefreshIntervalSec *int              `json:"refresh_interval_sec"`
}

func (s *Server) handleCreateExternalSource(w http.ResponseWriter, r *http.Request) {
	var body createExternalSourceBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	rawURL := strings.TrimSpace(body.URL)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "url required")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	interval := 0
	if body.RefreshIntervalSec != nil {
		interval = *body.RefreshIntervalSec
	}
	src := &store.ExternalSource{
		Name:               name,
		URL:                rawURL,
		Headers:            body.Headers,
		Enabled:            enabled,
		RefreshIntervalSec: interval,
	}
	if src.Headers == nil {
		src.Headers = map[string]string{}
	}
	if err := s.Store.CreateExternalSource(src); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Best-effort initial refresh so preview works immediately.
	if s.Aggregator != nil && src.Enabled {
		_ = s.Aggregator.RefreshSource(r.Context(), src.ID)
		if updated, err := s.Store.GetExternalSource(src.ID); err == nil {
			src = updated
		}
	}
	src.CachedBody = ""
	writeJSON(w, http.StatusCreated, src)
}

func (s *Server) handleUpdateExternalSource(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.Store.GetExternalSource(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body struct {
		Name               *string           `json:"name"`
		URL                *string           `json:"url"`
		Headers            map[string]string `json:"headers"`
		Enabled            *bool             `json:"enabled"`
		RefreshIntervalSec *int              `json:"refresh_interval_sec"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		existing.Name = strings.TrimSpace(*body.Name)
	}
	if body.URL != nil && strings.TrimSpace(*body.URL) != "" {
		existing.URL = strings.TrimSpace(*body.URL)
	}
	if body.Headers != nil {
		existing.Headers = body.Headers
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if body.RefreshIntervalSec != nil {
		existing.RefreshIntervalSec = *body.RefreshIntervalSec
	}
	if err := s.Store.UpdateExternalSource(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetExternalSource(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated.CachedBody = ""
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteExternalSource(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.Store.DeleteExternalSource(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefreshExternalSource(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, err := s.Store.GetExternalSource(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Aggregator == nil {
		writeError(w, http.StatusServiceUnavailable, "aggregator not configured")
		return
	}
	if err := s.Aggregator.RefreshSource(r.Context(), id); err != nil {
		src, _ := s.Store.GetExternalSource(id)
		if src != nil {
			src.CachedBody = ""
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":  err.Error(),
				"source": src,
			})
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	src, err := s.Store.GetExternalSource(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	src.CachedBody = ""
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) handlePreviewExternalSource(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	src, err := s.Store.GetExternalSource(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Aggregator == nil {
		writeError(w, http.StatusServiceUnavailable, "aggregator not configured")
		return
	}
	eps, warnings := s.Aggregator.EndpointsForSources(r.Context(), []store.ExternalSource{*src})
	names := make([]string, 0, len(eps))
	for _, ep := range eps {
		names = append(names, ep.Name)
	}
	// Refresh status fields after possible sync fetch.
	if updated, err := s.Store.GetExternalSource(id); err == nil {
		src = updated
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":    len(eps),
		"names":    names,
		"warnings": warnings,
		"source": map[string]any{
			"id":                 src.ID,
			"name":               src.Name,
			"content_type":       src.ContentType,
			"cached_proxy_count": src.CachedProxyCount,
			"last_error":         src.LastError,
			"last_success_unix":  src.LastSuccessUnix,
		},
	})
}
