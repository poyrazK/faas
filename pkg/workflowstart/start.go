// Package workflowstart creates a workflow run the way a manual start does:
// the plan gate, a definition snapshot from the app's live deployment (with
// published stored workflows applied), admission against the plan's run
// quotas and an optional idempotency key. apid's POST runs route and the
// gateway's guest workflow-runs route share it (ADR-20261113120824 §3), so a
// run started by a function is admitted exactly like one started by the API.
package workflowstart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the state the starter reads and writes.
type Store interface {
	LiveDeploymentForScope(ctx context.Context, appID, scope string) (state.Deployment, error)
	GetWorkflowRunByIdempotencyKey(ctx context.Context, appID, workflowName, key string, requestFingerprint []byte, tenantIDs ...string) (*state.WorkflowRun, error)
	CreateWorkflowRunAdmitted(ctx context.Context, r *state.WorkflowRun, maxActive, maxParked int) (int, error)
	CreateWorkflowRunAdmittedWithIdempotencyKey(ctx context.Context, r *state.WorkflowRun, maxActive, maxParked int, key string, requestFingerprint []byte) (int, bool, error)
}

// Request is one start.
type Request struct {
	Plan     api.Plan
	App      state.App
	Workflow string
	// Input is the run's JSON input; empty starts with {}.
	Input          json.RawMessage
	IdempotencyKey string
	TenantID       string
	// Scope is the deployment scope whose live deployment the run snapshots;
	// empty is the default (production) scope.
	Scope string
	// RuntimeDisabled refuses new runs (after the plan gate) while workflow
	// execution is off on this control plane.
	RuntimeDisabled bool
}

// Result is a started (or idempotently replayed) run, or the problem that
// refused it.
type Result struct {
	Run      *state.WorkflowRun
	Replayed bool
	Problem  *api.Problem
}

// Fingerprint is the idempotency fingerprint of a run's input.
func Fingerprint(input json.RawMessage) []byte {
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

var errIdempotencyConflict = api.NewProblem(http.StatusConflict, api.CodeConflict,
	"Idempotency key already used", "use a new Idempotency-Key when starting a run with different input")

// Start admits and creates the run. An error is a store failure the caller
// reports as a capacity problem; a refusal is Result.Problem.
func Start(ctx context.Context, store Store, req Request) (Result, error) {
	input := req.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	scope := req.Scope
	if scope == "" {
		scope = "default"
	}
	var fingerprint []byte
	if req.IdempotencyKey != "" {
		fingerprint = Fingerprint(input)
		original, err := store.GetWorkflowRunByIdempotencyKey(ctx, req.App.ID, req.Workflow, req.IdempotencyKey, fingerprint, req.TenantID)
		switch {
		case err == nil:
			return Result{Run: original, Replayed: true}, nil
		case errors.Is(err, state.ErrWorkflowRunIdempotencyConflict):
			return Result{Problem: errIdempotencyConflict}, nil
		case !errors.Is(err, state.ErrWorkflowRunNotFound):
			return Result{}, err
		}
	}
	if !req.Plan.WorkflowsAllowed() {
		return Result{Problem: api.ErrPlanWorkflowsNotAllowed(req.Plan)}, nil
	}
	if req.RuntimeDisabled {
		return Result{Problem: api.ErrWorkflowDeploymentUnavailable()}, nil
	}
	// Runs snapshot a definition from the live deployment, so a later deploy
	// never changes a run in flight and a name never deployed is refused.
	dep, err := store.LiveDeploymentForScope(ctx, req.App.ID, scope)
	if err != nil {
		// As apid always has: no live deployment in this scope (whatever the
		// lookup's error) means no definition to run.
		return Result{Problem: api.ErrWorkflowDefinitionNotFound()}, nil //nolint:nilerr // a refusal, not a failure
	}
	if stored, ok := store.(state.StoredWorkflowStore); ok {
		if dep.Workflows, err = stored.EffectiveWorkflowDefinitions(ctx, req.App.ID, dep.Workflows); err != nil {
			return Result{}, err
		}
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(dep.Workflows, &definitions); err != nil {
		return Result{}, err
	}
	var definition *api.WorkflowSpec
	for i := range definitions {
		if definitions[i].Name == req.Workflow {
			definition = &definitions[i]
			break
		}
	}
	if definition == nil {
		return Result{Problem: api.ErrWorkflowDefinitionNotFound()}, nil
	}
	snapshot, err := json.Marshal(definition)
	if err != nil {
		return Result{}, err
	}
	maxConcurrent, maxParked := req.Plan.WorkflowMaxConcurrentRuns(), req.Plan.WorkflowMaxParkedRuns()
	run := &state.WorkflowRun{
		DeploymentID: dep.ID, AppID: req.App.ID, PlatformTenantID: req.TenantID, WorkflowName: req.Workflow,
		Input: input, DefinitionSnapshot: snapshot, Status: state.WorkflowRunStatusPending, ScheduledFor: time.Now().UTC(),
	}
	var active int
	var replayed bool
	if req.IdempotencyKey != "" {
		active, replayed, err = store.CreateWorkflowRunAdmittedWithIdempotencyKey(ctx, run, maxConcurrent, maxParked, req.IdempotencyKey, fingerprint)
	} else {
		active, err = store.CreateWorkflowRunAdmitted(ctx, run, maxConcurrent, maxParked)
	}
	switch {
	case errors.Is(err, state.ErrWorkflowRunQuotaExceeded):
		return Result{Problem: api.ErrPlanWorkflowsQuota(req.Plan, maxConcurrent, active)}, nil
	case errors.Is(err, state.ErrWorkflowRunIdempotencyConflict):
		return Result{Problem: errIdempotencyConflict}, nil
	case errors.Is(err, state.ErrWorkflowDeploymentUnavailable):
		return Result{Problem: api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Workflow deployment changed", "the captured deployment is unavailable; retry starting a new run")}, nil
	case err != nil:
		return Result{}, err
	}
	return Result{Run: run, Replayed: replayed}, nil
}
