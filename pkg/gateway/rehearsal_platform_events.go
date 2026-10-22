// adr: 20261021120000
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// rehearsalEventBodyMaxBytes is apid's default JSON body cap for a single
// publish.
const rehearsalEventBodyMaxBytes int64 = 1 << 20

// serveRehearsalEventPublish captures POST /v1/events:publish into the run's
// event evidence (ADR-20261021120000), with the public API's validation and
// receipt.
func serveRehearsalEventPublish(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	body, problem := readRehearsalPlatformBody(w, r, rehearsalEventBodyMaxBytes, call.now)
	if problem != nil {
		if problem.Status == http.StatusRequestEntityTooLarge {
			problem = api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge, "Event too large",
				fmt.Sprintf("the event request is above the %d-byte cap; send large payloads by reference (for example an object key)", rehearsalEventBodyMaxBytes)).
				WithByteLimit(rehearsalEventBodyMaxBytes, rehearsalEventBodyMaxBytes+1).WithDocs("https://gregale.dev/docs/event-driven")
		}
		api.WriteProblem(w, problem)
		return
	}
	var req api.PublishEventRequest
	if decodeRehearsalStrictJSON(body, &req) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	receipt, problem, _ := captureRehearsalEvent(r.Context(), call, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("X-Gregale-Rehearsal", "captured")
	w.Header().Set("Location", receipt.ReceiptURL)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(receipt)
}

// serveRehearsalEventBatch captures POST /v1/events:publish-batch item by
// item, reporting each result in the public API's batch shape.
func serveRehearsalEventBatch(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	body, problem := readRehearsalPlatformBody(w, r, api.EventPublishBatchBodyMaxBytes, call.now)
	if problem != nil {
		if problem.Status == http.StatusRequestEntityTooLarge {
			problem = api.NewProblem(http.StatusRequestEntityTooLarge, "event_publish_batch_too_large", "Batch too large", "batch body exceeds the byte limit").
				WithLimit(api.EventPublishBatchBodyMaxBytes, api.EventPublishBatchBodyMaxBytes+1).WithDocs("https://gregale.dev/docs/event-driven#batch-event-publishing")
		}
		api.WriteProblem(w, problem)
		return
	}
	var wire struct {
		Events []json.RawMessage `json:"events"`
	}
	if decodeRehearsalStrictJSON(body, &wire) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid batch JSON body"))
		return
	}
	if len(wire.Events) == 0 || len(wire.Events) > api.EventPublishBatchMaxEvents {
		api.WriteProblem(w, api.ErrValidation("batch must contain 1–100 events").WithLimit(int64(api.EventPublishBatchMaxEvents), int64(len(wire.Events))).WithDocs("https://gregale.dev/docs/event-driven#batch-event-publishing"))
		return
	}
	response := api.PublishEventBatchResponse{Results: make([]api.PublishEventBatchResult, 0, len(wire.Events))}
	for index, raw := range wire.Events {
		result := api.PublishEventBatchResult{Index: index, Status: "accepted"}
		var req api.PublishEventRequest
		if json.Unmarshal(raw, &req) != nil {
			result.Status, result.Problem = "rejected", api.ErrValidation("invalid event JSON")
			response.Results = append(response.Results, result)
			continue
		}
		receipt, problem, duplicate := captureRehearsalEvent(r.Context(), call, req)
		switch {
		case problem != nil:
			result.Status, result.Problem, result.Retryable = "rejected", problem, problem.Status >= http.StatusInternalServerError
		case duplicate:
			result.Status, result.Receipt = "duplicate", &receipt
		default:
			result.Receipt = &receipt
		}
		response.Results = append(response.Results, result)
	}
	w.Header().Set("X-Gregale-Rehearsal", "captured")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// captureRehearsalEvent validates one publish as the public API does and
// records it. A repeat of a captured event is a duplicate, as in production.
func captureRehearsalEvent(ctx context.Context, call rehearsalPlatformCall, req api.PublishEventRequest) (api.PublishEventResponse, *api.Problem, bool) {
	envelope, err := events.NormalizeCapturedPublish(ctx, call.store, call.policy.AccountID, req, call.now)
	if err != nil {
		return api.PublishEventResponse{}, rehearsalEventProblem(err), false
	}
	captured, err := call.store.CaptureScenarioEvent(ctx, call.caller, call.policy.RunID, events.ScenarioCapture(envelope))
	duplicate := errors.Is(err, state.ErrScenarioEventDuplicate)
	switch {
	case duplicate:
	case errors.Is(err, state.ErrScenarioEventIdentityConflict):
		return api.PublishEventResponse{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Event identity conflict",
			"source and id already identify an event with different type, schema version, or data"), false
	case errors.Is(err, state.ErrScenarioActionsSealed):
		return api.PublishEventResponse{}, rehearsalSealedProblem(), false
	case err != nil || !captured:
		return api.PublishEventResponse{}, api.ErrCapacity("rehearsal event capture unavailable; retry the same source and id"), false
	}
	return api.PublishEventResponse{ReceiptURL: rehearsalEventReceiptURL(envelope.Source, envelope.ID), ID: envelope.ID,
		AcceptedAt: call.now, AccountID: call.policy.AccountID}, nil, duplicate
}

func rehearsalEventProblem(err error) *api.Problem {
	detail := err.Error()
	switch {
	case errors.Is(err, events.ErrPublishAccountMismatch):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Forbidden", "account_id must match the authenticated account")
	case errors.Is(err, events.ErrPublishInvalid):
		return api.ErrValidation(strings.TrimPrefix(detail, events.ErrPublishInvalid.Error()+": "))
	case errors.Is(err, state.ErrEventSchemaRequired) || errors.Is(err, state.ErrEventSchemaUnknown) || errors.Is(err, state.ErrEventDataInvalid):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Event schema validation failed", strings.TrimPrefix(detail, "validate event schema: "))
	}
	return api.ErrCapacity("failed to validate event schema")
}

// rehearsalEventReceiptURL mirrors the public API's receipt location.
func rehearsalEventReceiptURL(source, id string) string {
	return "/v1/events/receipt?" + url.Values{"source": {source}, "id": {id}}.Encode()
}

func rehearsalSealedProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, "rehearsal_actions_sealed", "Rehearsal actions sealed", "the rehearsal run no longer accepts platform work")
}

// readRehearsalPlatformBody reads a bounded request body under a deadline.
func readRehearsalPlatformBody(w http.ResponseWriter, r *http.Request, limit int64, now time.Time) ([]byte, *api.Problem) {
	if r.Body == nil {
		return nil, api.ErrValidation("empty request body")
	}
	_ = http.NewResponseController(w).SetReadDeadline(now.Add(5 * time.Second))
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	if err != nil {
		return nil, api.ErrValidation("read request body")
	}
	if int64(len(body)) > limit {
		return nil, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge, "Request too large",
			fmt.Sprintf("the request is above the %d-byte cap", limit)).WithByteLimit(limit, limit+1)
	}
	return body, nil
}
