package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Delays are fixture behavior, deliberately opt-in and shorter than the
// platform request deadline. No production setting or test result is changed.
func waitGitOpsRequestDelay(w http.ResponseWriter, r *http.Request) bool {
	raw := r.URL.Query().Get("gitops_delay_ms")
	if raw == "" {
		return true
	}
	milliseconds, err := strconv.Atoi(raw)
	if err != nil || milliseconds < 0 || milliseconds > 3000 {
		http.Error(w, "invalid fixture request delay", http.StatusBadRequest)
		return false
	}
	return waitGitOpsDelay(r, milliseconds)
}

func waitGitOpsQueueDelay(w http.ResponseWriter, r *http.Request) bool {
	var payload struct {
		Milliseconds int `json:"gitops_hold_ms"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil || payload.Milliseconds < 0 || payload.Milliseconds > 20000 {
		http.Error(w, "invalid fixture queue payload", http.StatusBadRequest)
		return false
	}
	return waitGitOpsDelay(r, payload.Milliseconds)
}

func waitGitOpsDelay(r *http.Request, milliseconds int) bool {
	if milliseconds == 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(milliseconds) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}
