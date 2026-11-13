package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowstart"
)

// FunctionWorkflowRunStore is the state the guest workflow-runs routes use.
type FunctionWorkflowRunStore interface {
	workflowstart.Store
	state.WorkflowRunStartStore
	AppByID(context.Context, string) (state.App, error)
	AppBySlug(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	GetWorkflowRun(context.Context, string) (*state.WorkflowRun, error)
	InsertWorkflowEvent(context.Context, *state.WorkflowEvent) error
}

// WithFunctionWorkflowRunAPI serves the guest workflow-runs routes on the
// platform host api.svc.gregale (ADR-20261113120824 §3):
//
//	POST /v1/workflow-runs             {function, input, workflow?}
//	GET  /v1/workflow-runs/{id}?wait=N
//	POST /v1/workflow-runs/{id}/events {event, payload}
//
// A function starts a workflow function it may call, admitted exactly like a
// manual start, in its own environment. Only the function that started a run
// reads it or sends it events; anything else is 404. The caller is resolved
// from its address, as for every service call. Other requests pass through.
func WithFunctionWorkflowRunAPI(next http.Handler, resolve ServiceProxyCallerIdentityResolver, store FunctionWorkflowRunStore) http.Handler {
	if resolve == nil || store == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := functionWorkflowRunRoute(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		callerApp, callerDeployment, err := resolve(r.Context(), r.RemoteAddr)
		if err != nil || callerApp == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Caller unknown", "workflow runs are started and read only from functions"))
			return
		}
		id, action, _ := strings.Cut(rest, "/")
		switch {
		case id == "" && r.Method == http.MethodPost:
			startFunctionWorkflowRun(w, r, store, callerApp, callerDeployment)
		case id != "" && action == "" && r.Method == http.MethodGet:
			readFunctionWorkflowRun(w, r, store, callerApp, id)
		case id != "" && action == "events" && r.Method == http.MethodPost:
			signalFunctionWorkflowRun(w, r, store, callerApp, id)
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such workflow-runs route"))
		}
	})
}

func functionWorkflowRunRoute(r *http.Request) (string, bool) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !strings.EqualFold(strings.TrimSuffix(host, "."), api.ScenarioPlatformAPIHost) {
		return "", false
	}
	if r.URL.Path == "/v1/workflow-runs" {
		return "", true
	}
	return strings.CutPrefix(r.URL.Path, "/v1/workflow-runs/")
}

type functionWorkflowStartRequest struct {
	Function string          `json:"function"`
	Workflow string          `json:"workflow,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

func startFunctionWorkflowRun(w http.ResponseWriter, r *http.Request, store FunctionWorkflowRunStore, callerAppID, callerDeploymentID string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, api.WorkflowRunInputMaxBytes+4096))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("failed to read request body"))
		return
	}
	var req functionWorkflowStartRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.Function == "" || (len(req.Input) > 0 && !json.Valid(req.Input)) {
		api.WriteProblem(w, api.ErrValidation("body must be JSON with function and an optional JSON input"))
		return
	}
	if len(req.Input) > api.WorkflowRunInputMaxBytes {
		api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowRunInputMaxBytes, len(req.Input)))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > state.WorkflowRunIdempotencyKeyMaxBytes || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		api.WriteProblem(w, api.ErrValidation("Idempotency-Key must contain 1 to 255 non-control bytes"))
		return
	}
	caller, err := store.AppByID(r.Context(), callerAppID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("caller unavailable"))
		return
	}
	slug, ok := caller.Manifest.FunctionCallables[req.Function]
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Function not callable",
			"function "+req.Function+" is not one this function may call"))
		return
	}
	target, err := store.AppBySlug(r.Context(), slug)
	if err != nil || target.AccountID != caller.AccountID || target.PlatformTenantRequired {
		api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
		return
	}
	account, err := store.AccountByID(r.Context(), target.AccountID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("account unavailable"))
		return
	}
	// The run lands in the caller's environment, as a send() does.
	scope := ""
	if callerDeploymentID != "" {
		deployment, err := store.DeploymentByID(r.Context(), callerDeploymentID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("caller environment unavailable"))
			return
		}
		scope = deployment.Scope
	}
	workflow := req.Workflow
	if workflow == "" {
		workflow = req.Function
	}
	result, err := workflowstart.Start(r.Context(), store, workflowstart.Request{
		Plan: account.Plan, App: target, Workflow: workflow, Input: req.Input, IdempotencyKey: key, Scope: scope,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to start workflow run"))
		return
	}
	if result.Problem != nil {
		api.WriteProblem(w, result.Problem)
		return
	}
	if err := store.RecordWorkflowRunStart(r.Context(), result.Run.ID, callerAppID); err != nil {
		// The run exists; a retry with the same Idempotency-Key replays it
		// and records the start again.
		api.WriteProblem(w, api.ErrCapacity("failed to record workflow run start"))
		return
	}
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeFunctionWorkflowRun(w, http.StatusCreated, result.Run)
}

// startedRun returns the run when callerAppID started it.
func startedRun(ctx context.Context, store FunctionWorkflowRunStore, callerAppID, id string) (*state.WorkflowRun, bool) {
	starter, err := store.WorkflowRunStarter(ctx, id)
	if err != nil || starter != callerAppID {
		return nil, false
	}
	run, err := store.GetWorkflowRun(ctx, id)
	if err != nil || run == nil {
		return nil, false
	}
	return run, true
}

var errFunctionWorkflowRunNotFound = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Workflow run not found", "no workflow run with this id was started by this function")

func readFunctionWorkflowRun(w http.ResponseWriter, r *http.Request, store FunctionWorkflowRunStore, callerAppID, id string) {
	run, ok := startedRun(r.Context(), store, callerAppID, id)
	if !ok {
		api.WriteProblem(w, errFunctionWorkflowRunNotFound)
		return
	}
	wait, problem := functionInvocationWait(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	deadline := time.Now().Add(wait)
	for !workflowRunDone(run.Status) && time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			deadline = time.Now()
		case <-time.After(functionInvocationPoll):
		}
		next, err := store.GetWorkflowRun(r.Context(), id)
		if err != nil || next == nil {
			break
		}
		run = next
	}
	writeFunctionWorkflowRun(w, http.StatusOK, run)
}

func signalFunctionWorkflowRun(w http.ResponseWriter, r *http.Request, store FunctionWorkflowRunStore, callerAppID, id string) {
	if _, ok := startedRun(r.Context(), store, callerAppID, id); !ok {
		api.WriteProblem(w, errFunctionWorkflowRunNotFound)
		return
	}
	var req struct {
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Event) == "" {
		api.WriteProblem(w, api.ErrValidation("body must be JSON with event and an optional payload"))
		return
	}
	err := store.InsertWorkflowEvent(r.Context(), &state.WorkflowEvent{RunID: id, EventName: req.Event, Payload: req.Payload})
	switch {
	case errors.Is(err, state.ErrWorkflowNotRunning):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Workflow run finished", "the run no longer accepts events"))
	case err != nil:
		api.WriteProblem(w, api.ErrValidation("event refused: "+err.Error()))
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func workflowRunDone(status string) bool {
	switch status {
	case state.WorkflowRunStatusSucceeded, state.WorkflowRunStatusFailed, state.WorkflowRunStatusDead, state.WorkflowRunStatusCancelled:
		return true
	}
	return false
}

func writeFunctionWorkflowRun(w http.ResponseWriter, status int, run *state.WorkflowRun) {
	out := api.FunctionWorkflowRunStatus{ID: run.ID, Status: run.Status, Done: workflowRunDone(run.Status)}
	if run.Status == state.WorkflowRunStatusSucceeded {
		out.Output = run.Output
	} else if out.Done && run.LastError != nil {
		out.Error = *run.LastError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}
