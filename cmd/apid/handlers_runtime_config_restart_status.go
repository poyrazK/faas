package main

import (
	"context"
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
	response, problem := s.readRuntimeConfigRestartStatus(r.Context(), app.ID, wakeID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// Called only after app ownership is established by the API or dashboard.
func (s *server) readRuntimeConfigRestartStatus(ctx context.Context, appID, wakeID string) (api.RuntimeConfigRestartStatusResponse, *api.Problem) {
	var response api.RuntimeConfigRestartStatusResponse
	reader, ok := s.notif.(runtimeConfigRestartStatusReader)
	if !ok {
		return response, api.ErrCapacity("restart status is unavailable")
	}
	status, err := reader.RuntimeConfigRestartStatus(ctx, appID, wakeID)
	if errors.Is(err, db.ErrRuntimeConfigRestartNotFound) {
		return response, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such runtime configuration restart")
	}
	if err != nil {
		if s.log != nil {
			s.log.Error("read runtime config restart status", "app", appID, "wake_id", wakeID, "err", err)
		}
		return response, api.ErrCapacity("could not read restart status")
	}
	return s.projectRuntimeConfigRestartStatus(appID, wakeID, status)
}

func (s *server) projectRuntimeConfigRestartStatus(appID, wakeID string, status db.RuntimeConfigRestartStatus) (api.RuntimeConfigRestartStatusResponse, *api.Problem) {
	if status.RequestedAt.IsZero() || status.Attempts < 0 || (status.State == "delivered" && (status.CompletedAt == nil || status.CompletedAt.IsZero())) {
		return api.RuntimeConfigRestartStatusResponse{}, api.ErrCapacity("restart status is unavailable")
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
			s.log.Error("unknown runtime config restart outbox state", "app", appID, "wake_id", wakeID, "state", status.State)
		}
		return api.RuntimeConfigRestartStatusResponse{}, api.ErrCapacity("restart status is unavailable")
	}
	if status.LastError != "" {
		response.FailureReason = runtimeConfigRestartFailureReason(status.LastError)
	}
	return response, nil
}

func runtimeConfigRestartFailureReason(lastError string) string {
	for _, reason := range []string{"telemetry_missing", "requests_active", "quiet_period_not_elapsed"} {
		if strings.Contains(lastError, "reason="+reason) {
			return reason
		}
	}
	return "restart_attempt_failed"
}
