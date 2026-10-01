package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
)

type exclusiveJobSubmissionKey struct{}

type exclusiveJobSubmission struct {
	policy, equivalenceKey string
	key                    json.RawMessage
}

type exclusiveAppTaskWorkRequest struct {
	Kind         string                   `json:"kind"`
	DeploymentID string                   `json:"deployment_id"`
	Task         api.CreateAppTaskRequest `json:"task"`
}

func (s *server) exclusiveStore(w http.ResponseWriter) (state.ExclusiveWorkStore, bool) {
	store, ok := s.store.(state.ExclusiveWorkStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed operation store unavailable"))
	}
	return store, ok
}

func writeExclusiveError(w http.ResponseWriter, err error) {
	var p *api.Problem
	switch {
	case errors.Is(err, exclusivework.ErrBusy):
		p = api.NewProblem(http.StatusConflict, "operation_busy", "Operation busy", "an earlier operation owns or is waiting for this key")
	case errors.Is(err, exclusivework.ErrIdentityConflict):
		p = api.NewProblem(http.StatusConflict, "operation_identity_conflict", "Operation identity conflict", "the idempotency key already identifies a different operation")
	case errors.Is(err, exclusivework.ErrStaleOwner):
		p = api.NewProblem(http.StatusConflict, "operation_ownership_lost", "Operation ownership lost", "the ownership grant is no longer current")
	case errors.Is(err, state.ErrNotFound):
		p = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Operation unavailable", "the operation, policy, or authorized scope is unavailable")
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		p = api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer suspended", "resume this customer before starting work")
	case errors.Is(err, state.ErrInvalidArgument):
		p = api.ErrValidation("invalid operation policy, key, or scope")
	case errors.Is(err, state.ErrQuotaExceeded):
		p = api.NewProblem(http.StatusTooManyRequests, "operation_quota_exceeded", "Operation quota exceeded", "the account operation limit has been reached")
	case errors.Is(err, state.ErrConflict):
		p = api.NewProblem(http.StatusConflict, "operation_policy_conflict", "Operation policy conflict", "an existing policy's ownership scope cannot change")
	default:
		p = api.ErrCapacity("managed operation transition failed")
	}
	api.WriteProblem(w, p)
}

func exclusiveAdmissionMetricOutcome(err error, joined, replayed bool) string {
	if err == nil {
		switch {
		case joined:
			return "joined"
		case replayed:
			return "replayed"
		default:
			return "accepted"
		}
	}
	if errors.Is(err, exclusivework.ErrBusy) || errors.Is(err, exclusivework.ErrIdentityConflict) ||
		errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrInvalidArgument) ||
		errors.Is(err, state.ErrPlatformTenantSuspended) || errors.Is(err, state.ErrQuotaExceeded) ||
		errors.Is(err, state.ErrConflict) {
		return "rejected"
	}
	return "error"
}

func (s *server) observeExclusiveAdmission(source string, err error, joined, replayed bool) {
	if s != nil && s.ops != nil {
		s.ops.ObserveExclusiveOperationAdmission(source, exclusiveAdmissionMetricOutcome(err, joined, replayed))
	}
}

func (s *server) upsertExclusivePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	var req api.ExclusivePolicyRequest
	if !decodeJSONLimit(w, r, &req, 16384) {
		return
	}
	if req.Name != "" && req.Name != r.PathValue("name") {
		api.WriteProblem(w, api.ErrValidation("policy name conflicts with route"))
		return
	}
	req.Name = r.PathValue("name")
	record, err := store.UpsertExclusiveWorkPolicy(r.Context(), acct.ID, exclusivework.Policy(req))
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *server) listExclusivePolicies(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	rows, err := store.ListExclusiveWorkPolicies(r.Context(), acct.ID)
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": rows})
}

func exclusiveTriggerBindingRecord(binding state.ExclusiveTriggerBinding) api.ExclusiveTriggerBindingRecord {
	return api.ExclusiveTriggerBindingRecord{Source: binding.Source, TriggerID: binding.TriggerID, AppID: binding.AppID, JobID: binding.JobID,
		Policy: binding.PolicyName, PlatformTenantID: binding.PlatformTenantID, Key: binding.Key,
		EquivalenceKey: binding.EquivalenceKey, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt}
}

func (s *server) upsertExclusiveTriggerBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ExclusiveTriggerBindingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation trigger binding store unavailable"))
		return
	}
	var req api.ExclusiveTriggerBindingRequest
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	binding, err := store.UpsertExclusiveTriggerBinding(r.Context(), state.ExclusiveTriggerBinding{
		Source: r.PathValue("source"), TriggerID: r.PathValue("id"), AccountID: acct.ID,
		PolicyName: req.Policy, PlatformTenantID: req.PlatformTenantID, Key: req.Key,
		EquivalenceKey: req.EquivalenceKey,
	})
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exclusiveTriggerBindingRecord(binding))
}

func (s *server) getExclusiveTriggerBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ExclusiveTriggerBindingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation trigger binding store unavailable"))
		return
	}
	binding, err := store.ExclusiveTriggerBinding(r.Context(), acct.ID, r.PathValue("source"), r.PathValue("id"))
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exclusiveTriggerBindingRecord(binding))
}

func (s *server) deleteExclusiveTriggerBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.ExclusiveTriggerBindingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation trigger binding store unavailable"))
		return
	}
	if err := store.DeleteExclusiveTriggerBinding(r.Context(), acct.ID, r.PathValue("source"), r.PathValue("id")); err != nil {
		writeExclusiveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	tenantID := r.PathValue("tenant_id")
	if tenantID != "" && strings.HasPrefix(r.URL.Path, "/v1/platform-tenant-self/") {
		tenants, ok := s.platformTenantStore(w, acct)
		if !ok {
			return
		}
		surfaces, err := tenants.ListPlatformTenantSurfaces(r.Context(), acct.ID, tenantID)
		if err != nil {
			writeExclusiveError(w, state.ErrNotFound)
			return
		}
		linked := false
		for _, surface := range surfaces {
			if surface.AppID == app.ID && surface.Status == state.SurfaceStatusActive {
				linked = true
				break
			}
		}
		if !linked {
			writeExclusiveError(w, state.ErrNotFound)
			return
		}
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("exclusive_operations", acct.Plan))
		return
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
		return
	}
	var req api.ExclusiveOperationRequest
	if !decodeJSONLimit(w, r, &req, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	if problem := validateInvokeRequest(req.Invocation); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.Invocation.Work != nil {
		api.WriteProblem(w, api.ErrValidation("choose a managed operation or an app work policy"))
		return
	}
	if req.Invocation.Method == "" {
		req.Invocation.Method = defaultInvokeMethod
	}
	if req.Invocation.Path == "" {
		req.Invocation.Path = "/"
	}
	if req.Invocation.DeadlineAt != nil || req.Invocation.RetryPolicy != nil || req.Invocation.RetentionSeconds != nil || req.Invocation.Destinations != nil {
		api.WriteProblem(w, api.ErrValidation("managed operations use the policy retry and deadline settings"))
		return
	}
	// Customer identity is an authorized account-owned route parameter.
	// Public customer ingress will supply verified context through its adapter.
	if tenantID != "" {
		tenants, ok := s.store.(state.PlatformTenantStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("customer scope store unavailable"))
			return
		}
		if _, err := tenants.GetPlatformTenant(r.Context(), acct.ID, tenantID); err != nil {
			writeExclusiveError(w, state.ErrNotFound)
			return
		}
	}
	var err error
	prepared := state.Invocation{AppID: app.ID, PlatformTenantID: tenantID, Method: req.Invocation.Method, Path: req.Invocation.Path, Payload: req.Invocation.Payload, Headers: req.Invocation.Headers}
	prepared.Headers, err = mergeInvocationVersionHeaders(prepared.Headers, r.Header)
	if err == nil {
		prepared, _, err = state.ResolveInvocationVersion(r.Context(), s.store, prepared)
	}
	if err != nil {
		var problem *api.Problem
		switch {
		case errors.Is(err, state.ErrInvalidArgument):
			problem = api.ErrValidation("invalid invocation version or headers")
		case errors.Is(err, state.ErrNotFound):
			problem = api.NewProblem(http.StatusGone, "invocation_version_unavailable", "Invocation version unavailable", "the requested revision or release is unavailable")
		case errors.Is(err, state.ErrConflict):
			problem = api.NewProblem(http.StatusConflict, "invocation_version_conflict", "Invocation version conflict", "the requested revision and release conflict")
		default:
			problem = api.ErrCapacity("resolve operation version")
		}
		api.WriteProblem(w, problem)
		return
	}
	req.Invocation.Headers = prepared.Headers
	request, err := json.Marshal(req.Invocation)
	if err != nil {
		writeExclusiveError(w, state.ErrInvalidArgument)
		return
	}
	op, joined, err := store.AdmitExclusiveOperation(r.Context(), state.ExclusiveAdmission{AccountID: acct.ID, AppID: app.ID, PlatformTenantID: tenantID, PolicyName: req.Policy, Key: req.Key, Request: request, EquivalenceKey: req.EquivalenceKey, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	s.observeExclusiveAdmission("manual", err, joined, op.Replayed)
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	statusURL := "/v1/operations/" + op.ID
	if strings.HasPrefix(r.URL.Path, "/v1/platform-tenant-self/") {
		statusURL = "/v1/platform-tenant-self/operations/" + op.ID
	}
	writeJSON(w, http.StatusAccepted, api.ExclusiveOperationAccepted{ID: op.ID, Joined: joined, StatusURL: statusURL})
}

// createExclusiveJobOperation reuses createJobRun's validation and
// normalization, then accepts the validated run through the exclusive lane.
// The regular /runs endpoint remains an explicitly unmanaged JobRun surface.
func (s *server) createExclusiveJobOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ExclusiveJobOperationRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	body, err := json.Marshal(req.Run)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid Job run request"))
		return
	}
	ctx := context.WithValue(r.Context(), exclusiveJobSubmissionKey{}, exclusiveJobSubmission{
		policy: req.Policy, key: req.Key, equivalenceKey: req.EquivalenceKey,
	})
	clone := r.Clone(ctx)
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	s.createJobRun(w, clone, acct)
}

func (s *server) createExclusiveAppTaskOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("exclusive_operations", acct.Plan))
		return
	}
	var req api.ExclusiveAppTaskOperationRequest
	if err := decodeJSONSized(r, &req, api.AppTaskRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AppTaskRequestMaxBytes, api.AppTaskRequestMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("invalid managed app task request body"))
		return
	}
	resolved, problem := req.Task.Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	deployment, err := s.store.LiveDeployment(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		writeAppTaskDeploymentUnavailable(w)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not select the app task deployment"))
		return
	}
	prepared := exclusiveAppTaskWorkRequest{Kind: "app_task", DeploymentID: deployment.ID, Task: api.CreateAppTaskRequest{
		Command: resolved.Command, CommandShell: resolved.CommandShell,
		TimeoutSeconds: resolved.TimeoutSeconds, MaxOutputBytes: resolved.MaxOutputBytes,
	}}
	request, err := json.Marshal(prepared)
	if err != nil {
		writeExclusiveError(w, state.ErrInvalidArgument)
		return
	}
	op, joined, err := store.AdmitExclusiveOperation(r.Context(), state.ExclusiveAdmission{
		AccountID: acct.ID, AppID: app.ID, PolicyName: req.Policy, Key: req.Key,
		Request: request, EquivalenceKey: req.EquivalenceKey, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	s.observeExclusiveAdmission("app_task", err, joined, op.Replayed)
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, api.ExclusiveOperationAccepted{
		ID: op.ID, Joined: joined, StatusURL: "/v1/operations/" + op.ID,
	})
}

func (s *server) getExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	op, err := store.ExclusiveOperationByID(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeExclusiveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *server) createPlatformTenantSelfExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	r.SetPathValue("tenant_id", tenantID)
	s.createExclusiveOperation(w, r, acct)
}

func (s *server) getPlatformTenantSelfExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	op, err := store.ExclusiveOperationByID(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil || op.PlatformTenantID != tenantID {
		writeExclusiveError(w, state.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, op)
}

func (s *server) cancelPlatformTenantSelfExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	op, err := store.ExclusiveOperationByID(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil || op.PlatformTenantID != tenantID {
		writeExclusiveError(w, state.ErrNotFound)
		return
	}
	if err = store.CancelExclusiveOperation(r.Context(), acct.ID, op.ID); err != nil {
		writeExclusiveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) cancelExclusiveOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.exclusiveStore(w)
	if !ok {
		return
	}
	if err := store.CancelExclusiveOperation(r.Context(), acct.ID, r.PathValue("id")); err != nil {
		writeExclusiveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
