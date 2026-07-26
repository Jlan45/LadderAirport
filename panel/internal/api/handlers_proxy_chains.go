package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/nodeconfig"
	"github.com/ladderairport/panel/internal/proxychain"
	"github.com/ladderairport/panel/internal/store"
)

func (s *Server) chainService() *proxychain.Service {
	if s.Chains != nil {
		return s.Chains
	}
	var builder = (*nodeconfig.Builder)(nil)
	var coordinator = (*sync.Mutex)(nil)
	if s.Runner != nil {
		builder = s.Runner.ConfigBuilder
		coordinator = s.Runner.Coordinator
	}
	s.Chains = proxychain.NewService(s.Store, builder, coordinator)
	return s.Chains
}

func (s *Server) handleListProxyChains(w http.ResponseWriter, _ *http.Request) {
	chains, err := s.Store.ListProxyChains()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chains)
}

func (s *Server) handleGetProxyChain(w http.ResponseWriter, r *http.Request) {
	chain, err := s.Store.GetProxyChain(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

func (s *Server) handleCreateProxyChain(w http.ResponseWriter, r *http.Request) {
	var chain store.ProxyChain
	if err := decodeJSON(r, &chain); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.Store.CreateProxyChain(&chain); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, _ := s.Store.GetProxyChain(chain.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateProxyChain(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	existing, err := s.Store.GetProxyChain(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var candidate store.ProxyChain
	if err := decodeJSON(r, &candidate); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	candidate.ID = id
	if existing.Enabled {
		candidate.Enabled = true
		ctx, cancel := s.chainOperationContext(r)
		defer cancel()
		if err := s.chainService().Deploy(ctx, candidate); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	} else {
		candidate.Enabled = false
		candidate.State = "disabled"
		if err := s.Store.UpdateProxyChain(&candidate); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	updated, _ := s.Store.GetProxyChain(id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteProxyChain(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteProxyChain(pathID(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnableProxyChain(w http.ResponseWriter, r *http.Request) {
	chain, err := s.Store.GetProxyChain(pathID(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := s.chainOperationContext(r)
	defer cancel()
	if err := s.chainService().Deploy(ctx, *chain); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	updated, _ := s.Store.GetProxyChain(chain.ID)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDisableProxyChain(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.chainOperationContext(r)
	defer cancel()
	if err := s.chainService().Disable(ctx, pathID(r)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	updated, _ := s.Store.GetProxyChain(pathID(r))
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleProbeProxyChain(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.Store.GetSettings()
	timeout := 10 * time.Second
	if settings != nil && settings.ChainProbeTimeoutSec > 0 {
		timeout = time.Duration(settings.ChainProbeTimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, err := s.chainService().Probe(ctx, pathID(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePreviewProxyChain(w http.ResponseWriter, r *http.Request) {
	var candidate store.ProxyChain
	if err := decodeJSON(r, &candidate); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	configs, err := s.chainService().Preview(candidate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := map[string]json.RawMessage{}
	for nodeID, cfg := range configs {
		out[nodeID] = json.RawMessage(cfg.JSON)
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": out})
}

func (s *Server) chainOperationContext(r *http.Request) (context.Context, context.CancelFunc) {
	settings, _ := s.Store.GetSettings()
	perHop := 10
	if settings != nil && settings.GRPCTimeoutSec > 0 {
		perHop = settings.GRPCTimeoutSec
	}
	return context.WithTimeout(r.Context(), time.Duration(perHop*24)*time.Second)
}
