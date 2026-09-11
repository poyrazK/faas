package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type readyResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
	Debugger string `json:"debugger,omitempty"`
	Error    string `json:"error,omitempty"`
}

// readyz is the dependency-aware readiness probe (issue #1037 follow-up / pre-1.0 readiness).
// It tests whether apid can actively reach PostgreSQL and acquire a connection from the pool.
// Returns HTTP 200 with {"status":"ready","database":"ok"} on success.
// Returns HTTP 503 with {"status":"unhealthy","database":"unreachable","error":"..."} on failure.
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")

	if s.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(readyResponse{
			Status:   "unhealthy",
			Database: "unconfigured",
			Error:    "store is nil",
		})
		return
	}

	if err := s.store.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(readyResponse{
			Status:   "unhealthy",
			Database: "unreachable",
			Error:    err.Error(),
		})
		return
	}
	debuggerChecked, err := debugRegressionReadinessCheck(ctx, s.store)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(readyResponse{
			Status:   "unhealthy",
			Database: "ok",
			Debugger: "unavailable",
			Error:    "debugger regression read probe failed",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	response := readyResponse{Status: "ready", Database: "ok"}
	if debuggerChecked {
		response.Debugger = "ready"
	}
	_ = json.NewEncoder(w).Encode(response)
}
