package api

import "net/http"

func (s *Server) handleListAutomationJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := s.Store.ListAutomationJobs(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleListAutomationAudits(w http.ResponseWriter, _ *http.Request) {
	logs, err := s.Store.ListAutomationAudits(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}
