// adr: 20261021150000
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// workflowStartRoute captures POST /v1/apps/{slug}/workflows/{name}/runs.
func workflowStartRoute(slug, workflow string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		header := r.Header.Get("Idempotency-Key")
		key := strings.TrimSpace(header)
		if header != "" && (key == "" || len(key) > state.WorkflowRunIdempotencyKeyMaxBytes || strings.IndexFunc(key, unicode.IsControl) >= 0) {
			api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("Idempotency-Key must contain 1 to %d non-control bytes", state.WorkflowRunIdempotencyKeyMaxBytes)))
			return
		}
		body, problem := readRehearsalPlatformBody(w, r, api.WorkflowRunInputMaxBytes, call.now)
		if problem != nil {
			if problem.Status == http.StatusRequestEntityTooLarge {
				problem = api.ErrRequestBodyTooLarge(api.WorkflowRunInputMaxBytes, api.WorkflowRunInputMaxBytes+1)
			}
			api.WriteProblem(w, problem)
			return
		}
		input := json.RawMessage(`{}`)
		if len(body) > 0 {
			if !json.Valid(body) {
				api.WriteProblem(w, api.ErrValidation("request body must be valid JSON"))
				return
			}
			input = body
		}
		if len(workflow) > 256 {
			api.WriteProblem(w, api.ErrWorkflowDefinitionNotFound())
			return
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: slug, Name: workflow, IdempotencyKey: key}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(map[string]json.RawMessage{"input": input}, len(input))
		result, problem := captureRehearsalPlatformCall(r, call, capture, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"),
			api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key already used", "use a new Idempotency-Key when starting a run with different input"))
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		now := call.now.Format(time.RFC3339)
		writeRehearsalCaptured(w, http.StatusCreated, api.WorkflowRunResponse{ID: result.ID, WorkflowName: workflow, Status: state.WorkflowRunStatusPending,
			Input: input, ScheduledFor: now, CreatedAt: now, UpdatedAt: now})
	}
}

// workflowEventRoute captures POST /v1/workflows/runs/{id}/events.
func workflowEventRoute(runID string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		body, problem := readRehearsalPlatformBody(w, r, rehearsalEventBodyMaxBytes, call.now)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		var req api.InjectWorkflowEventRequest
		if decodeRehearsalStrictJSON(body, &req) != nil {
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
		if len(req.EventName) > 256 {
			api.WriteProblem(w, api.ErrValidation("event_name must be at most 256 bytes"))
			return
		}
		fields := map[string]json.RawMessage{}
		if len(req.Payload) > 0 {
			fields["payload"] = req.Payload
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowEvent, Address: runID, Name: req.EventName, IdempotencyKey: r.Header.Get("Idempotency-Key")}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(fields, len(req.Payload))
		_, problem = captureRehearsalPlatformCall(r, call, capture, api.ErrWorkflowRunNotFound(),
			api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key already used", "this Idempotency-Key already delivered a different event to the run"))
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		writeRehearsalCaptured(w, http.StatusOK, api.InjectWorkflowEventResponse{Status: "received", EventName: req.EventName})
	}
}

// rehearsalPlatformMessage encodes a call's compared content; content over
// the capture bound is kept only as an incomplete marker.
func rehearsalPlatformMessage(fields map[string]json.RawMessage, size int) (json.RawMessage, bool) {
	if size > api.MaxScenarioCapturedPlatformCallBytes {
		return nil, true
	}
	message, _ := json.Marshal(fields)
	return message, false
}

func captureRehearsalPlatformCall(r *http.Request, call rehearsalPlatformCall, capture state.ScenarioPlatformCallCapture, notFound, conflict *api.Problem) (state.ScenarioPlatformCallResult, *api.Problem) {
	result, captured, err := call.store.CaptureScenarioPlatformCall(r.Context(), call.caller, capture)
	switch {
	case !captured || errors.Is(err, state.ErrNotFound):
		return result, notFound
	case errors.Is(err, state.ErrScenarioIdempotencyConflict):
		if conflict == nil {
			conflict = api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key conflict", "this Idempotency-Key was already used for a different request")
		}
		return result, conflict
	case errors.Is(err, state.ErrScenarioActionsSealed):
		return result, rehearsalSealedProblem()
	case err != nil:
		return result, api.ErrCapacity("rehearsal platform capture unavailable")
	}
	return result, nil
}

func writeRehearsalCaptured(w http.ResponseWriter, status int, body any) {
	w.Header().Set("X-Gregale-Rehearsal", "captured")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
