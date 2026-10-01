package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func workPolicyResponse(record state.AppWorkPolicy) api.WorkPolicyResponse {
	return api.WorkPolicyResponse{
		Name: record.Policy.Name, Revision: record.Revision,
		MaxRunningPerKey:         record.Policy.MaxRunningPerKey,
		MaxRunningPerFairnessKey: record.Policy.MaxRunningPerFairnessKey,
		PendingUpdates:           string(record.Policy.PendingUpdates),
		DebounceMS:               record.Policy.Debounce.Milliseconds(),
		ExpiresAfterMS:           record.Policy.ExpiresAfter.Milliseconds(),
		CreatedAt:                record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func (s *server) upsertWorkPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	environment, problem := s.workPolicyEnvironment(r, acct, app, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	policy, ok := decodeWorkPolicy(w, r)
	if !ok {
		return
	}
	if environment != "" {
		s.upsertEnvironmentWorkPolicy(w, r, acct, app, environment, policy)
		return
	}
	store, ok := s.store.(state.AppWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work policy store unavailable"))
		return
	}
	record, err := store.UpsertAppWorkPolicy(r.Context(), acct.ID, app.ID, policy)
	if err != nil {
		writeWorkPolicyProblem(w, err, "save work policy")
		return
	}
	writeJSON(w, http.StatusOK, workPolicyResponse(record))
}

func (s *server) listWorkPolicies(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	environment, problem := s.workPolicyEnvironment(r, acct, app, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if environment != "" {
		s.listEnvironmentWorkPolicies(w, r, app, environment)
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
	environment, problem := s.workPolicyEnvironment(r, acct, app, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if environment != "" {
		s.deleteEnvironmentWorkPolicy(w, r, acct, app, environment)
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

func (s *server) cancelPendingWork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	environment, problem := s.workPolicyEnvironment(r, acct, app, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if environment != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "invocation_environment_work_isolation_unavailable", "Stage work isolation unavailable", "stage cancellation requires isolated work lanes"))
		return
	}
	// Validate the selected environment before the legacy path-only receipt
	// lookup can replay a production cancellation as a successful stage action.
	s.idempotent(func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		s.cancelAppPendingWork(w, r, app)
	})(w, r, acct)
}

func (s *server) cancelAppPendingWork(w http.ResponseWriter, r *http.Request, app state.App) {
	var req api.CancelPendingWorkRequest
	if !decodeJSONLimit(w, r, &req, 1024) {
		return
	}
	key, err := workpolicy.CanonicalScalar(req.Key)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("work key must be a bounded string, number, or boolean"))
		return
	}
	policies, ok := s.store.(state.AppWorkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work policy store unavailable"))
		return
	}
	if _, err := policies.AppWorkPolicyByName(r.Context(), app.ID, r.PathValue("name")); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Work policy not found", "the app has no work policy with this name"))
		} else {
			api.WriteProblem(w, api.ErrCapacity("lookup work policy"))
		}
		return
	}
	cancellations, ok := s.store.(state.WorkCancellationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("work cancellation store unavailable"))
		return
	}
	receipt, err := cancellations.CancelPendingKeyedInvocations(r.Context(), app.ID, r.PathValue("name"), key, uuid.NewString())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("cancel pending work"))
		return
	}
	writeJSON(w, http.StatusOK, api.CancelPendingWorkResponse{ID: receipt.ID, CancelledCount: receipt.CancelledCount})
}

func decodeWorkPolicy(w http.ResponseWriter, r *http.Request) (workpolicy.Policy, bool) {
	var req api.UpsertWorkPolicyRequest
	if !decodeJSONLimit(w, r, &req, 4096) {
		return workpolicy.Policy{}, false
	}
	if req.DebounceMS < 0 || req.DebounceMS > int64(workpolicy.MaxDebounce/time.Millisecond) ||
		req.ExpiresAfterMS < 0 || req.ExpiresAfterMS > int64(workpolicy.MaxExpiresAfter/time.Millisecond) {
		api.WriteProblem(w, api.ErrValidation("work policy duration is out of range"))
		return workpolicy.Policy{}, false
	}
	policy := workpolicy.Policy{Name: r.PathValue("name"), MaxRunningPerKey: req.MaxRunningPerKey,
		MaxRunningPerFairnessKey: req.MaxRunningPerFairnessKey, PendingUpdates: workpolicy.PendingUpdates(req.PendingUpdates),
		Debounce: time.Duration(req.DebounceMS) * time.Millisecond, ExpiresAfter: time.Duration(req.ExpiresAfterMS) * time.Millisecond}
	if err := policy.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return workpolicy.Policy{}, false
	}
	return policy, true
}
