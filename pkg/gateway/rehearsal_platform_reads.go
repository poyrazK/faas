// adr: 20261022082721
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// rehearsalReadRoute serves reads of a run's own captures with the public
// API's response shapes (ADR-20261022082721). Captured work never runs, so it
// stays pending and has no deliveries; anything the run did not capture is
// the public API's not-found.
func rehearsalReadRoute(path string) (rehearsalPlatformRoute, bool) {
	switch path {
	case "/v1/events/receipt":
		return serveRehearsalEventReceipt, true
	case "/v1/executions":
		return serveRehearsalExecutionList, true
	case "/v1/executions/capabilities":
		return serveRehearsalExecutionCapabilities, true
	case "/v1/events/receipt/replays":
		return eventRecipientHistoryRoute("replays", "replay"), true
	case "/v1/events/receipt/attempts":
		return eventRecipientHistoryRoute("attempts", "attempt"), true
	case "/v1/events/recipient-blocker":
		return serveRehearsalRecipientBlocker, true
	}
	if slug, ok := strings.CutSuffix(strings.TrimPrefix(path, "/v1/apps/"), "/entities/inspect"); ok && strings.HasPrefix(path, "/v1/apps/") && slug != "" && !strings.Contains(slug, "/") {
		return entitiesUnavailableRoute, true
	}
	for prefix, serve := range map[string]func(string) rehearsalPlatformRoute{
		"/v1/invocations/":            invocationReadRoute,
		"/v1/delayed-tasks/":          delayedTaskReadRoute,
		"/v1/workflows/runs/":         workflowRunReadRoute,
		"/v1/executions/":             executionReadRoute,
		"/v1/cron-fire-now-requests/": cronRequestReadRoute,
	} {
		if id, ok := strings.CutPrefix(path, prefix); ok && id != "" && !strings.Contains(id, "/") {
			return serve(id), true
		}
	}
	return nil, false
}

func invocationReadRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		record, ok := readCapturedQueueWork(w, r, call, id)
		if !ok {
			return
		}
		inv := api.Invocation{ID: id, AccountID: call.policy.AccountID, Source: record.Kind, QueueName: record.QueueName,
			State: string(state.InvocationPending), Payload: capturedInvocationPayload(record), Headers: json.RawMessage(`{}`),
			DueAt: record.CreatedAt, ScheduledAt: record.ScheduledAt, CreatedAt: record.CreatedAt}
		// A send delivered within the run reports its invocation's state
		// (ADR-20261023111825).
		if record.Delivery == api.ScenarioQueueDelivered && record.DeliveryState != "" {
			inv.State = record.DeliveryState
		}
		if record.ScheduledAt != nil {
			inv.DueAt = *record.ScheduledAt
		}
		if record.Kind == api.ScenarioQueueKindAsyncInvoke {
			var invoke struct {
				Invoke api.InvokeRequest `json:"invoke"`
			}
			_ = json.Unmarshal(record.Message, &invoke)
			inv.Method, inv.Path = invoke.Invoke.Method, invoke.Invoke.Path
		}
		writeRehearsalCaptured(w, http.StatusOK, inv)
	}
}

func delayedTaskReadRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		record, ok := readCapturedQueueWork(w, r, call, id)
		if !ok {
			return
		}
		if record.Kind != api.ScenarioQueueKindDelayedTask {
			api.WriteProblem(w, api.ErrInvocationNotFound(id))
			return
		}
		scheduledAt := record.CreatedAt
		if record.ScheduledAt != nil {
			scheduledAt = *record.ScheduledAt
		}
		writeRehearsalCaptured(w, http.StatusOK, api.DelayedTaskResponse{ID: id, ScheduledAt: scheduledAt, State: string(state.InvocationPending), CreatedAt: record.CreatedAt})
	}
}

func readCapturedQueueWork(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, id string) (state.ScenarioCapturedQueueRecord, bool) {
	record, err := call.store.ScenarioCapturedQueueMessage(r.Context(), call.policy.AccountID, call.policy.RunID, id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return record, false
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("rehearsal capture unavailable"))
		return record, false
	}
	return record, true
}

// capturedInvocationPayload is the business payload a production invocation
// would carry, read from the compared message.
func capturedInvocationPayload(record state.ScenarioCapturedQueueRecord) json.RawMessage {
	var fields map[string]json.RawMessage
	if record.Oversized || json.Unmarshal(record.Message, &fields) != nil {
		return json.RawMessage(`null`)
	}
	for _, key := range []string{"payload", "inbox"} {
		if value, ok := fields[key]; ok {
			return value
		}
	}
	var invoke struct {
		Payload json.RawMessage `json:"payload"`
	}
	if raw, ok := fields["invoke"]; ok && json.Unmarshal(raw, &invoke) == nil && invoke.Payload != nil {
		return invoke.Payload
	}
	return json.RawMessage(`null`)
}

func workflowRunReadRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		record, err := call.store.ScenarioCapturedPlatformCall(r.Context(), call.policy.AccountID, call.policy.RunID, id)
		switch {
		case errors.Is(err, state.ErrNotFound) || err == nil && record.Kind != api.ScenarioPlatformKindWorkflowRun:
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
			return
		case err != nil:
			api.WriteProblem(w, api.ErrCapacity("rehearsal capture unavailable"))
			return
		}
		var start struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(record.Message, &start)
		at := record.CreatedAt.UTC().Format(time.RFC3339)
		writeRehearsalCaptured(w, http.StatusOK, api.WorkflowRunResponse{ID: id, WorkflowName: record.Name, Status: state.WorkflowRunStatusPending,
			Input: start.Input, ScheduledFor: at, CreatedAt: at, UpdatedAt: at})
	}
}

// serveRehearsalEventReceipt answers GET /v1/events/receipt for an event the
// run captured: accepted, never routed, with no recipients.
func serveRehearsalEventReceipt(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	guard, err := api.ParseAppEventAcceptanceGuard(r.URL.Query().Get("expected_accepted_at"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	source, id := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.URL.Query().Get("id"))
	if source == "" || id == "" || len(source) > 256 || len(id) > 256 {
		api.WriteProblem(w, api.ErrValidation("source and id are required and must be at most 256 bytes"))
		return
	}
	if problem, _ := api.ParseLimit(r.URL.Query().Get("limit"), 100, api.EventReceiptPageMax, "recipients"); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// A captured receipt has no recipients, so no page follows it
	// (ADR-20261022191742).
	if r.URL.Query().Get("after") != "" {
		api.WriteProblem(w, api.ErrValidation("invalid or mismatched event receipt cursor"))
		return
	}
	record, err := call.store.ScenarioCapturedEvent(r.Context(), call.policy.AccountID, call.policy.RunID, source, id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event receipt not found", "No retained receipt exists for this source and id"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("rehearsal capture unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if guard.ExpectedAcceptedAt != nil && !record.CreatedAt.Equal(*guard.ExpectedAcceptedAt) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "event_acceptance_replaced", "Event acceptance replaced", "the retained receipt belongs to a different acceptance"))
		return
	}
	writeRehearsalCaptured(w, http.StatusOK, api.EventReceiptResponse{EventID: id, EventSource: source, EventType: record.Type, SchemaVersion: record.SchemaVersion,
		AcceptedAt: record.CreatedAt, SnapshotCaptured: true, RoutingMode: "event", RoutingSummary: map[string]int{}, Recipients: []api.EventReceiptRecipientResponse{}})
}
