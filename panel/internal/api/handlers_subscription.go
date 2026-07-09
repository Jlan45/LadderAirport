package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/labberairport/panel/internal/store"
	"github.com/labberairport/panel/internal/subscription"
)

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]subView, 0, len(list))
	for i := range list {
		out = append(out, s.enrichSub(r, &list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

type createSubBody struct {
	Name       string   `json:"name"`
	Format     string   `json:"format"`
	InboundIDs []string `json:"inbound_ids"`
	Enabled    *bool    `json:"enabled"`
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var body createSubBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	format := strings.ToLower(strings.TrimSpace(body.Format))
	if format != "clash" && format != "singbox" {
		writeError(w, http.StatusBadRequest, "format must be clash or singbox")
		return
	}
	token, err := randomToken(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	sub := &store.Subscription{
		Name:       body.Name,
		Format:     format,
		Token:      token,
		InboundIDs: body.InboundIDs,
		Enabled:    enabled,
	}
	if err := s.Store.CreateSubscription(sub); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.enrichSub(r, sub))
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.Store.GetSubscription(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body struct {
		Name       *string  `json:"name"`
		Format     *string  `json:"format"`
		InboundIDs []string `json:"inbound_ids"`
		Enabled    *bool    `json:"enabled"`
		Rotate     bool     `json:"rotate_token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		existing.Name = *body.Name
	}
	if body.Format != nil {
		f := strings.ToLower(strings.TrimSpace(*body.Format))
		if f != "clash" && f != "singbox" {
			writeError(w, http.StatusBadRequest, "format must be clash or singbox")
			return
		}
		existing.Format = f
	}
	if body.InboundIDs != nil {
		existing.InboundIDs = body.InboundIDs
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if body.Rotate {
		tok, err := randomToken(16)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		existing.Token = tok
	}
	if err := s.Store.UpdateSubscription(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.Store.GetSubscription(id)
	writeJSON(w, http.StatusOK, s.enrichSub(r, updated))
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := s.Store.DeleteSubscription(id); err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewSubscription(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	sub, err := s.Store.GetSubscription(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, ctype, err := s.renderSubscription(sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handlePublicSubscription serves GET /sub/{token} without admin auth.
func (s *Server) handlePublicSubscription(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	sub, err := s.Store.GetSubscriptionByToken(token)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !sub.Enabled {
		writeError(w, http.StatusForbidden, "subscription disabled")
		return
	}
	body, ctype, err := s.renderSubscription(sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, subFilename(sub)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) renderSubscription(sub *store.Subscription) ([]byte, string, error) {
	nodes, err := s.Store.ListNodes()
	if err != nil {
		return nil, "", err
	}
	nodeInbounds := map[string][]store.InboundConfig{}
	for _, n := range nodes {
		ins, err := s.Store.ListInboundsForNode(n.ID)
		if err != nil {
			return nil, "", err
		}
		nodeInbounds[n.ID] = ins
	}
	eps, err := subscription.CollectEndpoints(nodes, nodeInbounds, sub.InboundIDs)
	if err != nil {
		return nil, "", err
	}
	switch sub.Format {
	case "clash":
		b, err := subscription.RenderClash(eps)
		return b, "text/yaml; charset=utf-8", err
	case "singbox":
		b, err := subscription.RenderSingbox(eps)
		return b, "application/json; charset=utf-8", err
	default:
		return nil, "", fmt.Errorf("unknown format %q", sub.Format)
	}
}

type subView struct {
	store.Subscription
	URL string `json:"url"`
}

func (s *Server) enrichSub(r *http.Request, sub *store.Subscription) subView {
	return subView{
		Subscription: *sub,
		URL:          s.subURL(r, sub.Token),
	}
}

func (s *Server) subURL(r *http.Request, token string) string {
	base := ""
	if st, err := s.Store.GetSettings(); err == nil {
		base = strings.TrimRight(st.PublicBaseURL, "/")
	}
	if base == "" && r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
			scheme = xf
		}
		host := r.Host
		if host == "" {
			host = "localhost"
		}
		base = scheme + "://" + host
	}
	if base == "" {
		return "/sub/" + token
	}
	return base + "/sub/" + token
}

func subFilename(sub *store.Subscription) string {
	if sub.Format == "clash" {
		return "clash.yaml"
	}
	return "singbox.json"
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
