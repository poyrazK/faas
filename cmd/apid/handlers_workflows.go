package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowRunResponse(r *state.WorkflowRun) api.WorkflowRunResponse {
	resp := api.WorkflowRunResponse{
		DeploymentID:     r.DeploymentID,
		ResumeCount:      r.ResumeCount,
		ID:               r.ID,
		AppID:            r.AppID,
		PlatformTenantID: r.PlatformTenantID,
		WorkflowName:     r.WorkflowName,
		Status:           r.Status,
		CurrentStep:      r.CurrentStep,
		Input:            r.Input,
		Output:           r.Output,
		ScheduledFor:     r.ScheduledFor.UTC().Format(time.RFC3339),
		LastError:        r.LastError,
		CreatedAt:        r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        r.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if r.CancelledAt != nil {
		v := r.CancelledAt.UTC().Format(time.RFC3339)
		resp.CancelledAt = &v
	}
	if r.StartedAt != nil {
		s := r.StartedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if r.FinishedAt != nil {
		s := r.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &s
	}
	return resp
}

func workflowStepResponse(s *state.WorkflowStep) api.WorkflowStepResponse {
	resp := api.WorkflowStepResponse{
		RetryBase:     s.RetryBase,
		ForEachParent: s.ForEachParent, ForEachIndex: s.ForEachIndex, ForEachCount: s.ForEachCount,
		WhenMatched: s.WhenMatched,
		SkipReason:  s.SkipReason,
		StepName:    s.StepName,
		Status:      s.Status,
		Attempt:     s.Attempt,
		Input:       s.Input,
		Output:      s.Output,
		Error:       s.Error,
		CreatedAt:   s.CreatedAt.UTC().Format(time.RFC3339),
	}
	if s.WhenEvaluatedAt != nil {
		evaluated := s.WhenEvaluatedAt.UTC().Format(time.RFC3339)
		resp.WhenEvaluatedAt = &evaluated
	}
	if s.StartedAt != nil {
		st := s.StartedAt.UTC().Format(time.RFC3339)
		resp.StartedAt = &st
	}
	if s.NextCheckAt != nil {
		next := s.NextCheckAt.UTC().Format(time.RFC3339)
		resp.NextCheckAt = &next
	}
	if s.FinishedAt != nil {
		ft := s.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &ft
	}
	return resp
}

func workflowStepAttemptResponse(a *state.WorkflowStepAttempt) api.WorkflowStepAttemptResponse {
	resp := api.WorkflowStepAttemptResponse{
		Attempt:    a.Attempt,
		Status:     a.Status,
		HTTPStatus: a.HTTPStatus,
		StartedAt:  a.StartedAt.UTC().Format(time.RFC3339),
		Error:      a.Error,
		Effects:    a.Effects,
	}
	if a.FinishedAt != nil {
		finished := a.FinishedAt.UTC().Format(time.RFC3339)
		resp.FinishedAt = &finished
	}
	if a.NextAttemptAt != nil {
		next := a.NextAttemptAt.UTC().Format(time.RFC3339)
		resp.NextAttemptAt = &next
	}
	return resp
}

// createWorkflowRun handles POST /v1/apps/{slug}/workflows/{name}/runs
func (s *server) createWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.createWorkflowRunWithTenant(w, r, acct, "")
}

func (s *server) createTenantWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		api.WriteProblem(w, api.ErrValidation("platform tenant ID is required"))
		return
	}
	s.createWorkflowRunWithTenant(w, r, acct, tenantID)
}

func (s *server) createPlatformTenantSelfWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	r.SetPathValue("tenant_id", tenantID)
	s.createTenantWorkflowRun(w, r, acct)
}

func (s *server) loadPlatformTenantSelfWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) (*state.WorkflowRun, bool) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return nil, false
	}
	run, err := s.store.GetWorkflowRun(r.Context(), r.PathValue("id"))
	if err != nil || run.PlatformTenantID != tenantID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	tenants, ok := s.store.(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("platform tenant store unavailable"))
		return nil, false
	}
	if err := state.ValidatePlatformTenantAppBinding(r.Context(), tenants, acct.ID, tenantID, app.ID); err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return run, true
}

func (s *server) getPlatformTenantSelfWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}

func (s *server) cancelPlatformTenantSelfWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, acct)
	if !ok {
		return
	}
	if run.Status != state.WorkflowRunStatusSucceeded && run.Status != state.WorkflowRunStatusFailed && run.Status != state.WorkflowRunStatusDead {
		var err error
		run, err = s.store.CancelWorkflowRun(r.Context(), run.ID, "cancelled by platform tenant")
		if err != nil {
			s.log.Error("cancel tenant workflow run failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to cancel workflow run"))
			return
		}
	}
	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}

func (s *server) createWorkflowRunWithTenant(w http.ResponseWriter, r *http.Request, acct state.Account, tenantID string) {
	slug := r.PathValue("slug")
	workflowName := r.PathValue("name")
	if workflowName == "" {
		api.WriteProblem(w, api.ErrValidation("workflow name is required"))
		return
	}

	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}

	keyHeader := r.Header.Get("Idempotency-Key")
	idempotencyKey := strings.TrimSpace(keyHeader)
	if keyHeader != "" && (idempotencyKey == "" || len(idempotencyKey) > state.WorkflowRunIdempotencyKeyMaxBytes || strings.IndexFunc(idempotencyKey, unicode.IsControl) >= 0) {
		api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("Idempotency-Key must contain 1 to %d non-control bytes", state.WorkflowRunIdempotencyKeyMaxBytes)))
		return
	}

	// Read and validate the request before resolving the current definition so
	// an idempotent retry can return its original run after a publish or deploy.
	r.Body = http.MaxBytesReader(w, r.Body, api.WorkflowRunInputMaxBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowRunInputMaxBytes, api.WorkflowRunInputMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("failed to read request body"))
		return
	}
	inputRaw := json.RawMessage(`{}`)
	if len(bodyBytes) > 0 {
		if !json.Valid(bodyBytes) {
			api.WriteProblem(w, api.ErrValidation("request body must be valid JSON"))
			return
		}
		inputRaw = bodyBytes
	}

	var requestFingerprint []byte
	if idempotencyKey != "" {
		requestFingerprint = workflowRunCreateRequestFingerprint(inputRaw)
		original, err := s.store.GetWorkflowRunByIdempotencyKey(r.Context(), app.ID, workflowName, idempotencyKey, requestFingerprint)
		if err == nil {
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSON(w, http.StatusCreated, workflowRunResponse(original))
			return
		}
		if errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Idempotency key already used", "use a new Idempotency-Key when starting a run with different input"))
			return
		}
		if !errors.Is(err, state.ErrWorkflowRunNotFound) {
			s.log.Error("look up idempotent workflow run failed", "app_id", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to look up workflow run idempotency key"))
			return
		}
	}

	// Gating: check plan allows workflows
	if !acct.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(acct.Plan))
		return
	}
	if !s.workflowRuntimeEnabled {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	if app.PlatformTenantRequired && tenantID == "" || tenantID != "" && !app.PlatformTenantRequired {
		api.WriteProblem(w, api.ErrWorkflowTenantIdentityUnavailable())
		return
	}
	if tenantID != "" {
		tenants, ok := s.store.(state.PlatformTenantStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("platform tenant store unavailable"))
			return
		}
		if err := state.ValidatePlatformTenantAppBinding(r.Context(), tenants, acct.ID, tenantID, app.ID); err != nil {
			if errors.Is(err, state.ErrPlatformTenantSuspended) || errors.Is(err, state.ErrNotFound) {
				api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
				return
			}
			api.WriteProblem(w, api.ErrCapacity("failed to verify workflow tenant access"))
			return
		}
	}

	// Runs must snapshot a definition from the current live deployment.
	// This keeps a run deterministic even when a later deployment changes
	// the workflow, and avoids accepting a name that was never deployed.
	dep, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, "default")
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
		return
	}
	if store, ok := s.store.(state.AutomationStore); ok {
		dep.Workflows, err = store.EffectiveWorkflowDefinitions(r.Context(), app.ID, dep.Workflows)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("failed to read published automations"))
			return
		}
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(dep.Workflows, &definitions); err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployed workflow definitions are invalid"))
		return
	}
	var definition *api.WorkflowSpec
	for i := range definitions {
		if definitions[i].Name == workflowName {
			definition = &definitions[i]
			break
		}
	}
	if definition == nil {
		api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
		return
	}
	defSnapshot, err := json.Marshal(definition)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to snapshot workflow definition"))
		return
	}

	maxConcurrent := acct.Plan.WorkflowMaxConcurrentRuns()

	run := &state.WorkflowRun{
		DeploymentID:       dep.ID,
		AppID:              app.ID,
		PlatformTenantID:   tenantID,
		WorkflowName:       workflowName,
		Input:              inputRaw,
		DefinitionSnapshot: defSnapshot,
		Status:             state.WorkflowRunStatusPending,
		ScheduledFor:       time.Now().UTC(),
	}

	var activeRuns int
	var replayed bool
	if idempotencyKey != "" {
		activeRuns, replayed, err = s.store.CreateWorkflowRunAdmittedWithIdempotencyKey(r.Context(), run, maxConcurrent, idempotencyKey, requestFingerprint)
	} else {
		activeRuns, err = s.store.CreateWorkflowRunAdmitted(r.Context(), run, maxConcurrent)
	}
	if errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
		api.WriteProblem(w, api.ErrPlanWorkflowsQuota(acct.Plan, maxConcurrent, activeRuns))
		return
	}
	if errors.Is(err, state.ErrWorkflowRunIdempotencyConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Idempotency key already used", "use a new Idempotency-Key when starting a run with different input"))
		return
	}
	if errors.Is(err, state.ErrWorkflowDeploymentUnavailable) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Workflow deployment changed", "the captured deployment is unavailable; retry starting a new run"))
		return
	}
	if err != nil {
		s.log.Error("create workflow run failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to persist workflow run"))
		return
	}

	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusCreated, workflowRunResponse(run))
}

func workflowRunCreateRequestFingerprint(input json.RawMessage) []byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	canonical := input
	if err := decoder.Decode(&value); err == nil {
		if encoded, err := json.Marshal(value); err == nil {
			canonical = encoded
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gregale.workflow-run.create:v1\x00"))
	_, _ = hash.Write(canonical)
	return hash.Sum(nil)
}

// listWorkflowRuns handles GET /v1/apps/{slug}/workflows/runs
func (s *server) listWorkflowRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}

	opts, validationProblem := workflowRunListOptionsFromQuery(r.URL.Query())
	if validationProblem != nil {
		api.WriteProblem(w, validationProblem)
		return
	}

	runs, total, err := s.store.ListWorkflowRuns(r.Context(), app.ID, opts)
	if err != nil {
		// codeql[go/log-injection] false-positive: request-derived IDs and errors are sanitized before they reach these structured log fields.
		s.log.Error("list workflow runs failed", "app_id", logsanitize.Field(app.ID), "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.ErrCapacity("failed to list workflow runs"))
		return
	}

	res := make([]api.WorkflowRunResponse, len(runs))
	for i, run := range runs {
		res[i] = workflowRunResponse(run)
	}

	writeJSON(w, http.StatusOK, api.ListWorkflowRunsResponse{
		Runs:  res,
		Total: total,
	})
}

func (s *server) listPlatformTenantSelfWorkflowRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, app, ok := s.platformTenantEventApp(w, r, acct)
	if !ok {
		return
	}
	opts, validationProblem := workflowRunListOptionsFromQuery(r.URL.Query())
	if validationProblem != nil {
		api.WriteProblem(w, validationProblem)
		return
	}
	opts.PlatformTenantID = tenantID
	runs, total, err := s.store.ListWorkflowRuns(r.Context(), app.ID, opts)
	if err != nil {
		// codeql[go/log-injection] false-positive: request-derived IDs and errors are sanitized before they reach these structured log fields.
		s.log.Error("list platform tenant workflow runs failed", "app_id", logsanitize.Field(app.ID), "platform_tenant_id", logsanitize.Field(tenantID), "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.ErrCapacity("failed to list workflow runs"))
		return
	}
	response := api.ListWorkflowRunsResponse{Runs: make([]api.WorkflowRunResponse, len(runs)), Total: total}
	for i, run := range runs {
		response.Runs[i] = workflowRunResponse(run)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func workflowRunListOptionsFromQuery(query map[string][]string) (state.ListWorkflowRunsOpts, *api.Problem) {
	opts := state.ListWorkflowRunsOpts{Limit: 50}
	opts.Status = ""
	if values, ok := query["status"]; ok && len(values) > 0 {
		opts.Status = values[0]
	}
	if !api.ValidWorkflowRunStatus(opts.Status) {
		return opts, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid workflow status",
			"status must be pending, running, awaiting_event, succeeded, failed, or dead")
	}
	if values, ok := query["workflow_name"]; ok {
		opts.WorkflowName = ""
		if len(values) > 0 {
			opts.WorkflowName = values[0]
		}
		if opts.WorkflowName == "" || len(opts.WorkflowName) > api.WorkflowWebhookNameMaxBytes {
			return opts, api.ErrValidation(fmt.Sprintf("workflow_name must contain 1 to %d bytes", api.WorkflowWebhookNameMaxBytes))
		}
	}
	var err error
	if opts.CreatedAfter, err = parseWorkflowRunTimeFilter(query, "created_after"); err != nil {
		return opts, api.ErrValidation("created_after must be an RFC3339 timestamp")
	}
	if opts.CreatedBefore, err = parseWorkflowRunTimeFilter(query, "created_before"); err != nil {
		return opts, api.ErrValidation("created_before must be an RFC3339 timestamp")
	}
	if opts.CreatedAfter != nil && opts.CreatedBefore != nil && opts.CreatedAfter.After(*opts.CreatedBefore) {
		return opts, api.ErrValidation("created_after must be earlier than or equal to created_before")
	}
	if values, ok := query["limit"]; ok && len(values) > 0 {
		if limit, err := strconv.Atoi(values[0]); err == nil && limit > 0 {
			if limit > 100 {
				limit = 100
			}
			opts.Limit = limit
		}
	}
	if values, ok := query["offset"]; ok && len(values) > 0 {
		if offset, err := strconv.Atoi(values[0]); err == nil && offset >= 0 {
			opts.Offset = offset
		}
	}
	return opts, nil
}

func parseWorkflowRunTimeFilter(query map[string][]string, name string) (*time.Time, error) {
	values, ok := query[name]
	if !ok {
		return nil, nil
	}
	if len(values) != 1 || values[0] == "" {
		return nil, fmt.Errorf("%s must be a single RFC3339 timestamp", name)
	}
	parsed, err := time.Parse(time.RFC3339Nano, values[0])
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

// getWorkflowRun handles GET /v1/workflows/runs/{id}
func (s *server) getWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrWorkflowRunNotFound) {
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
			return
		}
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow run"))
		return
	}

	// Verify account owns the app
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}

// listWorkflowSteps handles GET /v1/workflows/runs/{id}/steps
func (s *server) listWorkflowSteps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	steps, err := s.store.GetWorkflowSteps(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow steps"))
		return
	}

	res := make([]api.WorkflowStepResponse, len(steps))
	for i, step := range steps {
		res[i] = workflowStepResponse(step)
	}

	writeJSON(w, http.StatusOK, api.ListWorkflowStepsResponse{
		Steps: res,
	})
}

// listWorkflowStepAttempts handles GET /v1/workflows/runs/{id}/steps/{step}/attempts.
func (s *server) listWorkflowStepAttempts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	attempts, err := s.store.GetWorkflowStepAttempts(r.Context(), id, r.PathValue("step"))
	if errors.Is(err, state.ErrWorkflowStepNotFound) {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to get workflow step attempts"))
		return
	}
	res := make([]api.WorkflowStepAttemptResponse, len(attempts))
	for i, attempt := range attempts {
		res[i] = workflowStepAttemptResponse(attempt)
	}
	writeJSON(w, http.StatusOK, api.ListWorkflowStepAttemptsResponse{Attempts: res})
}

// injectWorkflowEvent handles POST /v1/workflows/runs/{id}/events
func (s *server) injectWorkflowEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	s.injectWorkflowEventForRun(w, r, run)
}

func (s *server) injectPlatformTenantSelfWorkflowEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, acct)
	if !ok {
		return
	}
	s.injectWorkflowEventForRun(w, r, run)
}

func (s *server) injectWorkflowEventForRun(w http.ResponseWriter, r *http.Request, run *state.WorkflowRun) {
	var req api.InjectWorkflowEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}

	if req.EventName == "" {
		api.WriteProblem(w, api.ErrValidation("event_name is required"))
		return
	}
	if api.IsWorkflowCallbackEventName(req.EventName) {
		api.WriteProblem(w, api.ErrValidation("callback event names are reserved; use the callback completion endpoint"))
		return
	}

	// Events may arrive before schedd claims a newly-created run, and recording
	// an unrelated event atomically wakes an awaiting run back to pending for
	// re-evaluation. Accept every active state while continuing to reject
	// terminal runs.
	if run.Status != state.WorkflowRunStatusPending &&
		run.Status != state.WorkflowRunStatusRunning &&
		run.Status != state.WorkflowRunStatusAwaitingEvent {
		api.WriteProblem(w, api.ErrWorkflowNotRunning())
		return
	}

	evt := &state.WorkflowEvent{
		RunID:     run.ID,
		EventName: req.EventName,
		Payload:   req.Payload,
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		evt.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(run.ID+"\x00"+key)).String()
	}
	var err error
	if run.PlatformTenantID != "" {
		continuations, ok := s.store.(state.TenantWorkflowContinuationStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("tenant workflow continuation store unavailable"))
			return
		}
		err = continuations.InsertTenantWorkflowEvent(r.Context(), run.PlatformTenantID, evt)
	} else {
		err = s.store.InsertWorkflowEvent(r.Context(), evt)
	}
	if err != nil {
		if errors.Is(err, state.ErrWorkflowRunNotFound) {
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
			return
		}
		if errors.Is(err, state.ErrWorkflowNotRunning) {
			api.WriteProblem(w, api.ErrWorkflowNotRunning())
			return
		}
		s.log.Error("record workflow event failed", "run_id", run.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record workflow event"))
		return
	}

	writeJSON(w, http.StatusOK, api.InjectWorkflowEventResponse{
		Status:    "received",
		EventName: req.EventName,
	})
}

func (s *server) workflowRunForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) (*state.WorkflowRun, bool) {
	run, err := s.store.GetWorkflowRun(r.Context(), r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	return run, true
}

func workflowCallbackSpecs(run *state.WorkflowRun) ([]api.WorkflowStepSpec, error) {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return nil, err
	}
	var callbacks []api.WorkflowStepSpec
	for _, step := range spec.Steps {
		if step.WaitForCallback {
			callbacks = append(callbacks, step)
		}
	}
	return callbacks, nil
}

// listWorkflowCallbacks returns stable, account-authorized callback handles.
func (s *server) listWorkflowCallbacks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.workflowRunForAccount(w, r, acct)
	if !ok {
		return
	}
	s.writeWorkflowCallbacks(w, r, run)
}

func (s *server) listPlatformTenantSelfWorkflowCallbacks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, acct)
	if !ok {
		return
	}
	s.writeWorkflowCallbacks(w, r, run)
}

func (s *server) writeWorkflowCallbacks(w http.ResponseWriter, r *http.Request, run *state.WorkflowRun) {
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return
	}
	steps, err := s.store.GetWorkflowSteps(r.Context(), run.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow steps"))
		return
	}
	started := make(map[string]time.Time, len(steps))
	for _, step := range steps {
		if step.StartedAt != nil {
			started[step.StepName] = *step.StartedAt
		}
	}
	response := api.ListWorkflowCallbacksResponse{Callbacks: make([]api.WorkflowCallbackResponse, 0, len(callbacks))}
	for _, step := range callbacks {
		entry := api.WorkflowCallbackResponse{ID: api.WorkflowCallbackID(run.ID, step.Name), StepName: step.Name}
		if activated, ok := started[step.Name]; ok {
			expires := activated.Add(step.Timeout).UTC().Format(time.RFC3339)
			entry.ExpiresAt = &expires
		}
		response.Callbacks = append(response.Callbacks, entry)
	}
	writeJSON(w, http.StatusOK, response)
}

// completeWorkflowCallback is authenticated like event injection. The ID is
// not a bearer token; no external caller gains authority from knowing it.
func (s *server) completeWorkflowCallback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.workflowRunForAccount(w, r, acct)
	if !ok {
		return
	}
	s.completeWorkflowCallbackForRun(w, r, run)
}

func (s *server) completePlatformTenantSelfWorkflowCallback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, ok := s.loadPlatformTenantSelfWorkflowRun(w, r, acct)
	if !ok {
		return
	}
	s.completeWorkflowCallbackForRun(w, r, run)
}

func (s *server) completeWorkflowCallbackForRun(w http.ResponseWriter, r *http.Request, run *state.WorkflowRun) {
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return
	}
	var selected *api.WorkflowStepSpec
	for i := range callbacks {
		if api.WorkflowCallbackID(run.ID, callbacks[i].Name) == r.PathValue("callback_id") {
			selected = &callbacks[i]
			break
		}
	}
	if selected == nil {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	var payload json.RawMessage
	if err := decodeJSON(r, &payload); err != nil {
		if !errors.Is(err, io.EOF) {
			api.WriteProblem(w, api.ErrValidation("callback body must be one JSON value within the request limit"))
			return
		}
	}
	eventName := api.WorkflowCallbackEventName(run.ID, selected.Name)
	var duplicate bool
	if run.PlatformTenantID != "" {
		continuations, ok := s.store.(state.TenantWorkflowContinuationStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("tenant workflow continuation store unavailable"))
			return
		}
		duplicate, err = continuations.CompleteTenantWorkflowCallback(r.Context(), run.PlatformTenantID, run.ID, selected.Name,
			eventName, r.PathValue("callback_id"), selected.Timeout, payload)
	} else {
		duplicate, err = s.store.CompleteWorkflowCallback(r.Context(), run.ID, selected.Name,
			eventName, r.PathValue("callback_id"), selected.Timeout, payload)
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrWorkflowRunNotFound):
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		case errors.Is(err, state.ErrWorkflowCallbackExpired):
			api.WriteProblem(w, api.ErrWorkflowCallbackExpired())
		case errors.Is(err, state.ErrWorkflowCallbackClosed):
			api.WriteProblem(w, api.ErrWorkflowCallbackClosed())
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.ErrWorkflowCallbackPayloadConflict())
		default:
			s.log.Error("complete workflow callback failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to complete workflow callback"))
		}
		return
	}
	writeJSON(w, http.StatusOK, api.CompleteWorkflowCallbackResponse{Status: "received", Duplicate: duplicate})
}

// cancelWorkflowRun handles POST /v1/workflows/runs/{id}/cancel
func (s *server) cancelWorkflowRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	run, err := s.store.GetWorkflowRun(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}

	if run.Status != state.WorkflowRunStatusSucceeded &&
		run.Status != state.WorkflowRunStatusFailed &&
		run.Status != state.WorkflowRunStatusDead {
		cancelErr := "cancelled by operator"
		run, err = s.store.CancelWorkflowRun(r.Context(), run.ID, cancelErr)
		if err != nil {
			s.log.Error("cancel workflow run failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("failed to cancel workflow run"))
			return
		}
	}

	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}

// retryWorkflowStep resumes a safe failed HTTP step within its existing run.
func (s *server) retryWorkflowStep(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(acct.Plan))
		return
	}
	if !s.workflowRuntimeEnabled {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	run, err := s.store.GetWorkflowRun(r.Context(), r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	retryStore, ok := s.store.(state.WorkflowRetryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow step retry is unavailable"))
		return
	}
	maxActive := acct.Plan.WorkflowMaxConcurrentRuns()
	run, active, err := retryStore.RetryWorkflowStep(r.Context(), run.ID, r.PathValue("step"), maxActive)
	if errors.Is(err, state.ErrWorkflowRunNotFound) {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	if errors.Is(err, state.ErrWorkflowRetryNotAllowed) {
		api.WriteProblem(w, api.ErrWorkflowStepRetryNotAllowed())
		return
	}
	if errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
		api.WriteProblem(w, api.ErrPlanWorkflowsQuota(acct.Plan, maxActive, active))
		return
	}
	if err != nil {
		s.log.Error("retry workflow step failed", "run_id", r.PathValue("id"), "step", r.PathValue("step"), "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to retry workflow step"))
		return
	}
	writeJSON(w, http.StatusOK, workflowRunResponse(run))
}
