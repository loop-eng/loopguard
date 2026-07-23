package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")

	if s.backend == nil {
		http.Error(w, `{"error":"daemon not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	detail, err := s.backend.GetSession(id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := json.NewEncoder(w).Encode(detail); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")

	if s.backend == nil {
		http.Error(w, `{"error":"daemon not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if err := s.backend.KillSession(r.Context(), id); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "killed",
		"session": id,
	}); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.backend == nil {
		http.Error(w, `{"error":"daemon not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	snapshot := s.backend.GetConfig()
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := StatusResponse{Daemon: "running"}
	if s.backend != nil {
		resp.Sessions = s.backend.GetSessions()
	}
	if resp.Sessions == nil {
		resp.Sessions = []SessionInfo{}
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var sessions []SessionInfo
	if s.backend != nil {
		sessions = s.backend.GetSessions()
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}

	if err := json.NewEncoder(w).Encode(sessions); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.PathValue("id")

	if s.backend == nil {
		http.Error(w, `{"error":"daemon not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if err := s.backend.ResumeSession(r.Context(), id); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "resumed",
		"session": id,
	}); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}
