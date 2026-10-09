package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getWorkflowRunDiagnostics(w http.ResponseWriter, r *http.Request, account state.Account) {
	run, ok := s.loadOwnedWorkflowRun(w, r, account)
	if !ok {
		return
	}
	s.writeWorkflowRunDiagnostics(w, r, account, run, "")
}

func (s *server) getPlatformTenantSelfWorkflowRunDiagnostics(w http.ResponseWriter, r *http.Request, account state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, account)
	if !ok {
		return
	}
	s.writeWorkflowRunDiagnostics(w, r, account, run, run.PlatformTenantID)
}

func (s *server) writeWorkflowRunDiagnostics(w http.ResponseWriter, r *http.Request, account state.Account, run *state.WorkflowRun, tenantID string) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.WorkflowRunDiagnosticsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow diagnostics storage unavailable"))
		return
	}
	result, err := store.GetWorkflowRunDiagnostics(r.Context(), state.WorkflowDiagnosticsOptions{RunID: run.ID, AccountID: account.ID, PlatformTenantID: tenantID})
	if errors.Is(err, state.ErrWorkflowRunNotFound) {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow diagnostics"))
		return
	}
	if !s.workflowRuntimeEnabled {
		result.Resume.Eligible = false
		result.Resume.Blockers = append(result.Resume.Blockers, api.WorkflowRecoveryBlocker("runtime_disabled", ""))
	}
	writeJSON(w, http.StatusOK, result)
}
