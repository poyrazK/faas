package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getRuntimeConfigRestartStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	wakeID := strings.TrimSpace(r.PathValue("wake_id"))
	if wakeID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid wake_id", "wake_id path segment is required"))
		return
	}
	reader, ok := s.notif.(runtimeConfigRestartStatusReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("restart status is unavailable"))
		return
	}
	status, err := reader.RuntimeConfigRestartStatus(r.Context(), app.ID, wakeID)
	if errors.Is(err, db.ErrRuntimeConfigRestartNotFound) {
		s.notFound(w, "no such runtime configuration restart")
		return
	}
	if err != nil {
		if s.log != nil {
			s.log.Error("read runtime config restart status", "app", app.ID, "wake_id", wakeID, "err", err)
		}
		api.WriteProblem(w, api.ErrCapacity("could not read restart status"))
		return
	}

	response := api.RuntimeConfigRestartStatusResponse{
		WakeID:      wakeID,
		Attempts:    status.Attempts,
		RequestedAt: status.RequestedAt,
		CompletedAt: status.CompletedAt,
	}
	switch status.State {
	case "pending":
		response.Status = "queued"
		if status.Attempts > 0 {
			response.Status = "retrying"
		}
	case "processing":
		response.Status = "running"
	case "delivered":
		response.Status = "completed"
	case "dead_letter":
		response.Status = "failed"
	default:
		if s.log != nil {
			s.log.Error("unknown runtime config restart outbox state", "app", app.ID, "wake_id", wakeID, "state", status.State)
		}
		api.WriteProblem(w, api.ErrCapacity("restart status is unavailable"))
		return
	}
	if status.LastError != "" {
		response.FailureReason = runtimeConfigRestartFailureReason(status.LastError)
	}
	writeJSON(w, http.StatusOK, response)
}

func runtimeConfigRestartFailureReason(lastError string) string {
	for _, reason := range []string{"telemetry_missing", "requests_active", "quiet_period_not_elapsed"} {
		if strings.Contains(lastError, "reason="+reason) {
			return reason
		}
	}
	return "restart_attempt_failed"
}
