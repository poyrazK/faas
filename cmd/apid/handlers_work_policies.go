package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func workPolicyResponse(record state.AppWorkPolicy) api.WorkPolicyResponse {
	return api.WorkPolicyResponse{
		Name: record.Policy.Name, Revision: record.Revision,
		MaxRunningPerKey: record.Policy.MaxRunningPerKey,
		PendingUpdates:   string(record.Policy.PendingUpdates),
		DebounceMS:       record.Policy.Debounce.Milliseconds(),
		ExpiresAfterMS:   record.Policy.ExpiresAfter.Milliseconds(),
		CreatedAt:        record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func (s *server) upsertWorkPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.UpsertWorkPolicyRequest
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	policy := workpolicy.Policy{Name: r.PathValue("name"),
		MaxRunningPerKey: req.MaxRunningPerKey,
		PendingUpdates:   workpolicy.PendingUpdates(req.PendingUpdates),
		Debounce:         time.Duration(req.DebounceMS) * time.Millisecond,
		ExpiresAfter:     time.Duration(req.ExpiresAfterMS) * time.Millisecond}
	if req.DebounceMS < 0 || req.DebounceMS > int64(workpolicy.MaxDebounce/time.Millisecond) ||
		req.ExpiresAfterMS < 0 || req.ExpiresAfterMS > int64(workpolicy.MaxExpiresAfter/time.Millisecond) {
		api.WriteProblem(w, api.ErrValidation("work policy duration is out of range"))
		return
	}
	if err := policy.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.AppWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work policy store unavailable"))
		return
	}
	record, err := store.UpsertAppWorkPolicy(r.Context(), acct.ID, app.ID, policy)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "the app is unavailable"))
		return
	}
	if errors.Is(err, state.ErrQuotaExceeded) {
		api.WriteProblem(w, api.ErrValidation("maximum work policies per app reached"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("save work policy"))
		return
	}
	writeJSON(w, http.StatusOK, workPolicyResponse(record))
}

func (s *server) listWorkPolicies(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AppWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work policy store unavailable"))
		return
	}
	records, err := store.ListAppWorkPolicies(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list work policies"))
		return
	}
	out := api.WorkPolicyListResponse{Policies: make([]api.WorkPolicyResponse, 0, len(records))}
	for _, record := range records {
		out.Policies = append(out.Policies, workPolicyResponse(record))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteWorkPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AppWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work policy store unavailable"))
		return
	}
	err := store.DeleteAppWorkPolicy(r.Context(), acct.ID, app.ID, r.PathValue("name"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Work policy not found", "the app has no work policy with this name"))
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "work_policy_in_use", "Work policy in use", "remove event subscriptions using this policy before deleting it"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("delete work policy"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
