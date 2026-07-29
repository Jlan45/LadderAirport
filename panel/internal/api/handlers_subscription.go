package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/ladderairport/panel/internal/nodeconfig"
	"github.com/ladderairport/panel/internal/store"
	"github.com/ladderairport/panel/internal/subscription"
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
	Name               string   `json:"name"`
	Format             string   `json:"format"`
	InboundIDs         []string `json:"inbound_ids"`
	IncludeAllInbounds *bool    `json:"include_all_inbounds"`
	IncludeStandalone  *bool    `json:"include_standalone"`
	ChainIDs           []string `json:"chain_ids"`
	IncludeAllChains   *bool    `json:"include_all_chains"`
	ExternalSourceIDs  []string `json:"external_source_ids"`
	Enabled            *bool    `json:"enabled"`
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var body createSubBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "必须提供名称")
		return
	}
	format := strings.ToLower(strings.TrimSpace(body.Format))
	token, err := randomToken(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	// Backward compatibility: legacy clients use an empty inbound_ids array for
	// "all local inbounds". New clients send include_all_inbounds explicitly so
	// false + [] can represent an external-only subscription.
	includeAll := len(body.InboundIDs) == 0
	if body.IncludeAllInbounds != nil {
		includeAll = *body.IncludeAllInbounds
	}
	settings, _ := s.Store.GetSettings()
	migrated := settings != nil && settings.ChainSubscriptionMigrated
	includeStandalone := !migrated
	if body.IncludeStandalone != nil {
		includeStandalone = *body.IncludeStandalone
	}
	includeAllChains := migrated
	if body.IncludeAllChains != nil {
		includeAllChains = *body.IncludeAllChains
	}
	sub := &store.Subscription{
		Name:               body.Name,
		Format:             format,
		Token:              token,
		InboundIDs:         body.InboundIDs,
		IncludeAllInbounds: includeAll,
		IncludeStandalone:  includeStandalone,
		ChainIDs:           body.ChainIDs,
		IncludeAllChains:   includeAllChains,
		Enabled:            enabled,
	}
	if err := s.Store.CreateSubscription(sub); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.ExternalSourceIDs != nil {
		if err := s.Store.SetSubscriptionExternalSources(sub.ID, body.ExternalSourceIDs); err != nil {
			// Association replacement is transactional. Remove the just-created
			// subscription as well so a 400 response never leaves a hidden row behind.
			if cleanupErr := s.Store.DeleteSubscription(sub.ID); cleanupErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("%v（清理失败：%v）", err, cleanupErr))
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
		Name               *string  `json:"name"`
		Format             *string  `json:"format"`
		InboundIDs         []string `json:"inbound_ids"`
		IncludeAllInbounds *bool    `json:"include_all_inbounds"`
		IncludeStandalone  *bool    `json:"include_standalone"`
		ChainIDs           []string `json:"chain_ids"`
		IncludeAllChains   *bool    `json:"include_all_chains"`
		ExternalSourceIDs  []string `json:"external_source_ids"`
		Enabled            *bool    `json:"enabled"`
		Rotate             bool     `json:"rotate_token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return
	}
	before := *existing
	before.InboundIDs = append([]string{}, existing.InboundIDs...)
	before.ChainIDs = append([]string{}, existing.ChainIDs...)
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		existing.Name = *body.Name
	}
	if body.Format != nil {
		existing.Format = strings.ToLower(strings.TrimSpace(*body.Format))
	}
	if body.InboundIDs != nil {
		existing.InboundIDs = body.InboundIDs
		if body.IncludeAllInbounds == nil {
			// Preserve legacy update semantics: [] meant all, a non-empty list meant selected.
			existing.IncludeAllInbounds = len(body.InboundIDs) == 0
		}
	}
	if body.IncludeAllInbounds != nil {
		existing.IncludeAllInbounds = *body.IncludeAllInbounds
	}
	if body.IncludeStandalone != nil {
		existing.IncludeStandalone = *body.IncludeStandalone
	}
	if body.ChainIDs != nil {
		existing.ChainIDs = body.ChainIDs
		if body.IncludeAllChains == nil {
			existing.IncludeAllChains = len(body.ChainIDs) == 0
		}
	}
	if body.IncludeAllChains != nil {
		existing.IncludeAllChains = *body.IncludeAllChains
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
	if body.ExternalSourceIDs != nil {
		if err := s.Store.SetSubscriptionExternalSources(id, body.ExternalSourceIDs); err != nil {
			if rollbackErr := s.Store.UpdateSubscription(&before); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("%v（回滚失败：%v）", err, rollbackErr))
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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

func detectFormat(r *http.Request) string {
	if r == nil {
		return "v2ray"
	}
	q := r.URL.Query()
	// 1. Explicit query parameter check: format, flag, target
	for _, key := range []string{"format", "flag", "target"} {
		if val := strings.ToLower(strings.TrimSpace(q.Get(key))); val != "" {
			switch val {
			case "clash", "mihomo", "clashmeta":
				return "clash"
			case "singbox", "sing-box":
				return "singbox"
			case "v2ray", "v2rayn", "v2rayng", "base64", "links":
				return "v2ray"
			}
		}
	}
	// Check boolean flag presence in query string
	if q.Has("clash") {
		return "clash"
	}
	if q.Has("singbox") || q.Has("sing-box") {
		return "singbox"
	}
	if q.Has("v2ray") {
		return "v2ray"
	}

	// 2. User-Agent header inspection
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	if ua != "" {
		for _, kw := range []string{"clash", "mihomo", "stash", "openclash"} {
			if strings.Contains(ua, kw) {
				return "clash"
			}
		}
		for _, kw := range []string{"sing-box", "singbox", "sbox", "sfi", "sfa", "sfo", "sfm"} {
			if strings.Contains(ua, kw) {
				return "singbox"
			}
		}
		for _, kw := range []string{"v2ray", "v2rayn", "v2rayng", "shadowrocket", "quantumult", "surge", "nekobox", "passwall"} {
			if strings.Contains(ua, kw) {
				return "v2ray"
			}
		}
	}

	// 3. Fallback format
	return "v2ray"
}

func parseUseDomain(r *http.Request) bool {
	if r == nil {
		return true
	}
	q := r.URL.Query()
	for _, key := range []string{"use_domain", "domain", "fqdn", "use_fqdn"} {
		if val := strings.ToLower(strings.TrimSpace(q.Get(key))); val != "" {
			switch val {
			case "false", "0", "no", "off":
				return false
			case "true", "1", "yes", "on":
				return true
			}
		}
	}
	if q.Get("no_domain") == "true" || q.Get("no_domain") == "1" {
		return false
	}
	return true
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
	format := detectFormat(r)
	useDomain := parseUseDomain(r)
	body, ctype, err := s.renderSubscription(r.Context(), sub, format, useDomain)
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
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	sub, err := s.Store.GetSubscriptionByToken(token)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	if !sub.Enabled {
		writeError(w, http.StatusForbidden, "订阅已禁用")
		return
	}
	format := detectFormat(r)
	useDomain := parseUseDomain(r)
	body, ctype, err := s.renderSubscription(r.Context(), sub, format, useDomain)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Profile-Update-Interval", "24")
	w.Header().Set("Content-Disposition", contentDisposition(subFilename(sub, format)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) renderSubscription(ctx context.Context, sub *store.Subscription, format string, useDomain bool) ([]byte, string, error) {
	nodes, err := s.Store.ListNodes()
	if err != nil {
		return nil, "", err
	}
	nodeAttachments := map[string][]store.NodeInboundAttachment{}
	for _, n := range nodes {
		atts, err := s.Store.ListNodeInboundAttachments(n.ID)
		if err != nil {
			return nil, "", err
		}
		nodeAttachments[n.ID] = atts
	}
	local := []subscription.ProxyEndpoint{}
	if sub.IncludeStandalone && (sub.IncludeAllInbounds || len(sub.InboundIDs) > 0) {
		filter := sub.InboundIDs
		if sub.IncludeAllInbounds {
			filter = nil
		}
		local, err = subscription.CollectEndpointsFromAttachments(nodes, nodeAttachments, filter)
		if err != nil {
			return nil, "", err
		}
		nodeManagedDomains := map[string]string{}
		if mds, err := s.Store.ListManagedDomains(); err == nil {
			for _, md := range mds {
				if md.Enabled && strings.TrimSpace(md.FQDN) != "" && md.NodeID != "" {
					nodeManagedDomains[md.NodeID] = strings.TrimSpace(md.FQDN)
				}
			}
		}

		builder := &nodeconfig.Builder{Store: s.Store}
		for i := range local {
			nodeID := local[i].Node.ID
			boundDomain := nodeManagedDomains[nodeID]

			resolved, hostname, managed, err := builder.ResolveManagedTLS(
				nodeID, local[i].Inbound,
			)
			if err != nil {
				return nil, "", err
			}
			if managed && hostname != "" {
				verify := false
				local[i].Inbound = resolved
				local[i].Params = resolved.Params
				if useDomain {
					local[i].Server = hostname
				} else if local[i].Node.Address != "" {
					local[i].Server = strings.TrimSpace(local[i].Node.Address)
				}
				local[i].TLSSkipVerify = &verify
			} else if useDomain && boundDomain != "" && !subscription.IsDomainHost(local[i].Server) {
				local[i].Server = boundDomain
			} else if !useDomain && subscription.IsDomainHost(local[i].Server) && local[i].Node.Address != "" {
				local[i].Server = strings.TrimSpace(local[i].Node.Address)
			}
		}
	}
	if sub.IncludeAllChains || len(sub.ChainIDs) > 0 {
		chains, err := s.Store.ListProxyChains()
		if err != nil {
			return nil, "", err
		}
		filter := map[string]bool{}
		for _, id := range sub.ChainIDs {
			filter[id] = true
		}
		builder := &nodeconfig.Builder{Store: s.Store}
		for _, chain := range chains {
			if !chain.Enabled || (!sub.IncludeAllChains && !filter[chain.ID]) {
				continue
			}
			ep, err := builder.ResolveHopEndpoint(chain, 0)
			if err != nil {
				return nil, "", err
			}
			ep.Name = chain.Name
			ep.SourceName = "链式代理"
			local = append(local, ep)
		}
	}

	var external []subscription.ProxyEndpoint
	if s.Aggregator != nil {
		sources, err := s.Store.ListExternalSourcesForSubscription(sub.ID)
		if err != nil {
			return nil, "", err
		}
		// EndpointsForSources soft-fails individual sources.
		external, _ = s.Aggregator.EndpointsForSources(ctx, sources)
	}
	eps := subscription.MergeEndpointsContext(ctx, local, external)
	if len(eps) == 0 {
		return nil, "", fmt.Errorf("没有可用的代理端点，请检查节点地址、入站关联和外部源")
	}

	switch format {
	case "clash":
		b, err := subscription.RenderClash(eps)
		return b, "text/yaml; charset=utf-8", err
	case "singbox":
		b, err := subscription.RenderSingbox(eps)
		return b, "application/json; charset=utf-8", err
	case "v2ray":
		b, err := subscription.RenderV2ray(eps)
		return b, "text/plain; charset=utf-8", err
	default:
		b, err := subscription.RenderV2ray(eps)
		return b, "text/plain; charset=utf-8", err
	}
}

type subView struct {
	store.Subscription
	URL               string   `json:"url"`
	ExternalSourceIDs []string `json:"external_source_ids"`
}

func (s *Server) enrichSub(r *http.Request, sub *store.Subscription) subView {
	ids, err := s.Store.ListExternalSourceIDsForSubscription(sub.ID)
	if err != nil || ids == nil {
		ids = []string{}
	}
	return subView{
		Subscription:      *sub,
		URL:               s.subURL(r, sub.Token),
		ExternalSourceIDs: ids,
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

func subFilename(sub *store.Subscription, format string) string {
	base := sanitizeFilename(sub.Name)
	if base == "" {
		base = "subscription"
	}
	switch format {
	case "clash":
		return base + ".yaml"
	case "singbox":
		return base + ".json"
	case "v2ray":
		return base + ".txt"
	default:
		return base + ".txt"
	}
}

// contentDisposition builds a Content-Disposition value with ASCII fallback + UTF-8 filename*.
func contentDisposition(filename string) string {
	ascii := asciiFilenameFallback(filename)
	// RFC 5987 filename*
	encoded := url.PathEscape(filename)
	// PathEscape uses %20 for space; RFC 5987 prefers %20 which is fine.
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, encoded)
}

func asciiFilenameFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > unicode.MaxASCII || r < 32 || r == '"' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), ".-_ ")
	if out == "" {
		// Keep extension if present.
		if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
			ext := name[i:]
			safeExt := ""
			for _, r := range ext {
				if r > unicode.MaxASCII || r < 32 {
					continue
				}
				safeExt += string(r)
			}
			if safeExt != "" && safeExt != "." {
				return "subscription" + safeExt
			}
		}
		return "subscription.bin"
	}
	return out
}

// sanitizeFilename keeps a safe basename for Content-Disposition.
// Strips path separators and control chars; collapses whitespace to '-'.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	prevDash := false
	for _, r := range name {
		switch {
		case r < 32 || r == 127:
			continue
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|' || r == '\'' || r == ';':
			continue
		case r == ' ' || r == '\t':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			b.WriteRune(r)
			prevDash = false
		}
	}
	out := strings.Trim(b.String(), ".-_")
	// Avoid overly long filenames (clients / FS limits).
	runes := []rune(out)
	if len(runes) > 80 {
		out = string(runes[:80])
		out = strings.TrimRight(out, ".-_")
	}
	return out
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
