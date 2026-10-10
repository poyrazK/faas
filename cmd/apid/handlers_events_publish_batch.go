package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func (s *server) publishEventBatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var wire struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := decodeJSONSized(r, &wire, api.EventPublishBatchBodyMaxBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "event_publish_batch_too_large", "Batch too large", "batch body exceeds the byte limit").WithLimit(api.EventPublishBatchBodyMaxBytes, api.EventPublishBatchBodyMaxBytes+1).WithDocs("https://gregale.dev/docs/event-driven#batch-event-publishing"))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid batch JSON body"))
		}
		return
	}
	if len(wire.Events) == 0 || len(wire.Events) > api.EventPublishBatchMaxEvents {
		api.WriteProblem(w, api.ErrValidation("batch must contain 1–100 events").WithLimit(int64(api.EventPublishBatchMaxEvents), int64(len(wire.Events))).WithDocs("https://gregale.dev/docs/event-driven#batch-event-publishing"))
		return
	}
	store, ok := s.store.(state.PublishedEventAcceptanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acceptance store"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventPublishBatchTimeout)
	defer cancel()
	response := api.PublishEventBatchResponse{Results: make([]api.PublishEventBatchResult, 0, len(wire.Events))}
	for index, raw := range wire.Events {
		result := s.publishEventBatchItem(ctx, store, acct, raw)
		result.Index = index
		response.Results = append(response.Results, result)
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) publishEventBatchItem(ctx context.Context, store state.PublishedEventAcceptanceStore, acct state.Account, raw json.RawMessage) api.PublishEventBatchResult {
	if ctx.Err() != nil {
		return batchPublishFailure("rejected", true, api.NewProblem(http.StatusServiceUnavailable, "event_publish_not_attempted", "Event not attempted", "retry the same source and id"))
	}
	var req api.PublishEventRequest
	if json.Unmarshal(raw, &req) != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation("invalid event JSON"))
	}
	if req.AccountID != "" && req.AccountID != acct.ID {
		return batchPublishFailure("rejected", false, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Forbidden", "account_id must match the authenticated account"))
	}
	envelope, err := normalizePublishRequest(req, acct.ID)
	if err != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation(err.Error()))
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		return batchPublishFailure("rejected", false, api.ErrValidation("gregale.* sources are reserved for platform events"))
	}
	if problem, retryable := s.batchEventSchemaProblem(ctx, acct.ID, req); problem != nil {
		return batchPublishFailure("rejected", retryable, problem)
	}
	trace := pkgtrace.InjectHeaders(ctx)
	envelope.Traceparent, envelope.Tracestate, envelope.Baggage = trace["traceparent"], trace["tracestate"], trace["baggage"]
	payload, err := json.Marshal(envelope)
	if err != nil {
		return batchPublishFailure("rejected", false, api.ErrValidation("invalid event data"))
	}
	accepted, err := store.AcceptPublishedEvent(ctx, "apid", acct.ID, payload)
	if err != nil {
		return batchEventAcceptanceFailure(err)
	}
	status := "accepted"
	if accepted.Duplicate {
		status = "duplicate"
	}
	return api.PublishEventBatchResult{Status: status, Receipt: &api.PublishEventResponse{ID: envelope.ID, AccountID: acct.ID, AcceptedAt: accepted.AcceptedAt, ReceiptURL: eventReceiptURL(envelope.Source, envelope.ID)}}
}

func (s *server) batchEventSchemaProblem(ctx context.Context, accountID string, req api.PublishEventRequest) (*api.Problem, bool) {
	registry, ok := s.store.(state.EventSchemaStore)
	if !ok {
		return api.ErrInternal("event schema registry"), true
	}
	err := state.ValidatePublishedEventSchema(ctx, registry, accountID, strings.TrimSpace(req.Source), strings.TrimSpace(req.Type), req.SchemaVersion, req.Data)
	if err == nil {
		return nil, false
	}
	if errors.Is(err, state.ErrEventSchemaRequired) || errors.Is(err, state.ErrEventSchemaUnknown) || errors.Is(err, state.ErrEventDataInvalid) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Event schema validation failed", err.Error()), false
	}
	return api.ErrCapacity("failed to validate event schema; retry the same source and id"), true
}

func batchEventAcceptanceFailure(err error) api.PublishEventBatchResult {
	var pgErr *pgconn.PgError
	if errors.Is(err, state.ErrConflict) || (errors.As(err, &pgErr) && pgErr.ConstraintName == "event_fanout_identity_uniq") {
		return batchPublishFailure("rejected", false, api.NewProblem(http.StatusConflict, api.CodeConflict, "Event identity conflict", "source and id already identify different type, schema version, or data"))
	}
	if problem := eventStorageCapacityProblem(err); problem != nil {
		return batchPublishFailure("rejected", true, problem)
	}
	// An interrupted commit may have succeeded. Do not report a definite rejection.
	return batchPublishFailure("unknown", true, api.ErrCapacity("acceptance could not be confirmed; retry the same source and id"))
}

func batchPublishFailure(status string, retryable bool, problem *api.Problem) api.PublishEventBatchResult {
	return api.PublishEventBatchResult{Status: status, Retryable: retryable, Problem: problem}
}
