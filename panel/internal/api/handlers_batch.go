package api

import (
	"net/http"

	"github.com/labberairport/panel/internal/store"
)

type batchRequest struct {
	NodeIDs []string `json:"node_ids"`
	Labels  []string `json:"labels"`
}

func (s *Server) handleBatchApply(w http.ResponseWriter, r *http.Request) {
	s.runBatchTask(w, r, "apply")
}

func (s *Server) handleBatchStart(w http.ResponseWriter, r *http.Request) {
	s.runBatchTask(w, r, "start")
}

func (s *Server) handleBatchStop(w http.ResponseWriter, r *http.Request) {
	s.runBatchTask(w, r, "stop")
}

func (s *Server) runBatchTask(w http.ResponseWriter, r *http.Request, taskType string) {
	var req batchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	nodeIDs, err := s.resolveBatchTargets(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(nodeIDs) == 0 {
		writeError(w, http.StatusBadRequest, "no nodes matched node_ids or labels")
		return
	}
	if s.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "runner not configured")
		return
	}
	task := &store.Task{
		Type:    taskType,
		Status:  "pending",
		NodeIDs: nodeIDs,
	}
	if err := s.Store.CreateTask(task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Runner.RunTask(r.Context(), task.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Store.GetTask(task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// resolveBatchTargets returns the union of explicit node IDs and nodes matching any label.
func (s *Server) resolveBatchTargets(req batchRequest) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range req.NodeIDs {
		add(id)
	}
	if len(req.Labels) > 0 {
		nodes, err := s.Store.ListNodesByLabels(req.Labels)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			add(n.ID)
		}
	}
	return out, nil
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	task, err := s.Store.GetTask(id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}
