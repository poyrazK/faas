package appevents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func (s *Service) PublishEventBatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var wire struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := httpkit.DecodeJSONSized(r, &wire, api.EventPublishBatchBodyMaxBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "event_publish_batch_too_large", "Batch too large", "batch body exceeds the byte limit").WithLimit(api.EventPublishBatchBodyMaxBytes, api.EventPublishBatchBodyMaxBytes+1).WithDocs("https://gregale.dev/docs/events#batch-event-publishing"))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid batch JSON body"))
		}
		return
	}
	if len(wire.Events) == 0 || len(wire.Events) > api.EventPublishBatchMaxEvents {
		api.WriteProblem(w, api.ErrValidation("batch must contain 1–100 events").WithLimit(int64(api.EventPublishBatchMaxEvents), int64(len(wire.Events))).WithDocs("https://gregale.dev/docs/events#batch-event-publishing"))
		return
	}
	store, ok := s.Host.Store().(state.PublishedEventAcceptanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acceptance store"))
		return
	}
	producer, problem := s.accountProducer(r.Context(), acct)
	if problem == nil {
		problem = s.PublishRateProblem(r.Context(), acct, len(wire.Events))
	}
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventPublishBatchTimeout)
	defer cancel()
	response := api.PublishEventBatchResponse{Results: make([]api.PublishEventBatchResult, len(wire.Events))}
	// Validate every event first, then accept the valid ones together: one
	// transaction for the batch when the store can, not one per event
	// (2026-11-12 throughput test: ~15–30 events/s per account).
	pending := make([]preparedBatchEvent, 0, len(wire.Events))
	for index, raw := range wire.Events {
		prepared, result := s.prepareEventBatchItem(ctx, acct, producer, raw)
		if prepared.payload == nil {
			result.Index = index
			response.Results[index] = result
			continue
		}
		prepared.index = index
		pending = append(pending, prepared)
	}
	s.acceptEventBatchItems(ctx, store, acct, pending, response.Results)
	_ = s.Host.Notify(r.Context(), db.NotifyEventPublished, "1")
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, response)
}

// preparedBatchEvent is a batch event that passed validation and still needs
// acceptance.
type preparedBatchEvent struct {
	index    int
	envelope events.Envelope
	payload  []byte
}

func eventNotAttempted() api.PublishEventBatchResult {
	return batchPublishFailure("rejected", true, api.NewProblem(http.StatusServiceUnavailable, "event_publish_not_attempted", "Event not attempted", "retry the same source and id"))
}

// acceptEventBatchItems accepts the prepared events in one store call when
// the store accepts batches, else one at a time, and fills their results.
// An event the batch deadline reached before acceptance began is reported
// not attempted; one whose acceptance may have committed is reported unknown.
func (s *Service) acceptEventBatchItems(ctx context.Context, store state.PublishedEventAcceptanceStore, acct state.Account, pending []preparedBatchEvent, results []api.PublishEventBatchResult) {
	settle := func(p preparedBatchEvent, result api.PublishEventBatchResult) {
		result.Index = p.index
		results[p.index] = result
	}
	if batch, ok := store.(state.PublishedEventBatchAcceptanceStore); ok && len(pending) > 0 {
		if ctx.Err() != nil {
			for _, p := range pending {
				settle(p, eventNotAttempted())
			}
			return
		}
		payloads := make([][]byte, len(pending))
		for i, p := range pending {
			payloads[i] = p.payload
		}
		accepted, err := batch.AcceptPublishedEventBatch(ctx, "apid", acct.ID, payloads)
		for i, p := range pending {
			outcome := state.PublishedEventBatchAcceptance{Err: err}
			if err == nil {
				outcome = accepted[i]
			}
			settle(p, batchEventAcceptanceResult(acct.ID, p.envelope, outcome))
		}
		return
	}
	for _, p := range pending {
		if ctx.Err() != nil {
			settle(p, eventNotAttempted())
			continue
		}
		accepted, err := store.AcceptPublishedEvent(ctx, "apid", acct.ID, p.payload)
		settle(p, batchEventAcceptanceResult(acct.ID, p.envelope, state.PublishedEventBatchAcceptance{Acceptance: accepted, Err: err}))
	}
}

func batchEventAcceptanceResult(accountID string, envelope events.Envelope, outcome state.PublishedEventBatchAcceptance) api.PublishEventBatchResult {
	if outcome.Err != nil {
		return batchEventAcceptanceFailure(outcome.Err)
	}
	status := "accepted"
	if outcome.Acceptance.Duplicate {
		status = "duplicate"
	}
	return api.PublishEventBatchResult{Status: status, Receipt: &api.PublishEventResponse{ID: envelope.ID, AccountID: accountID, AcceptedAt: outcome.Acceptance.AcceptedAt, ReceiptURL: EventReceiptURL(envelope.Source, envelope.ID)}}
}

// prepareEventBatchItem validates one batch event. A prepared event with a
// payload is ready for acceptance; without one, the result is final
// (rejected, or a duplicate already accepted).
func (s *Service) prepareEventBatchItem(ctx context.Context, acct state.Account, producer string, raw json.RawMessage) (preparedBatchEvent, api.PublishEventBatchResult) {
	if ctx.Err() != nil {
		return preparedBatchEvent{}, eventNotAttempted()
	}
	var req api.PublishEventRequest
	if json.Unmarshal(raw, &req) != nil {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.ErrValidation("invalid event JSON"))
	}
	if req.AccountID != "" && req.AccountID != acct.ID {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Forbidden", "account_id must match the authenticated account"))
	}
	envelope, err := NormalizePublishRequest(req, acct.ID)
	if err != nil {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.ErrValidation(err.Error()))
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.ErrValidation("gregale.* sources are reserved for platform events"))
	}
	if _, reserved := eventcontract.AppSource(envelope.Source); reserved {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, AppSourceReservedProblem())
	}
	environment, problem := s.PublishEnvironment(ctx, acct.ID, req.EnvironmentID)
	if problem != nil {
		return preparedBatchEvent{}, batchPublishFailure("rejected", problem.Status >= http.StatusInternalServerError, problem)
	}
	envelope.EnvironmentID = environment
	envelope.Producer = producer
	trace := pkgtrace.InjectHeaders(ctx)
	envelope.Traceparent, envelope.Tracestate, envelope.Baggage = trace["traceparent"], trace["tracestate"], trace["baggage"]
	payload, err := json.Marshal(envelope)
	if err != nil {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.ErrValidation("invalid event data"))
	}
	if lookup, ok := s.Host.Store().(state.PublishedEventLookupStore); ok {
		accepted, lookupErr := lookup.LookupPublishedEventAcceptance(ctx, acct.ID, payload)
		if lookupErr == nil {
			return preparedBatchEvent{}, api.PublishEventBatchResult{Status: "duplicate", Receipt: &api.PublishEventResponse{ID: envelope.ID, AccountID: acct.ID, AcceptedAt: accepted.AcceptedAt, ReceiptURL: EventReceiptURL(envelope.Source, envelope.ID)}}
		}
		if !errors.Is(lookupErr, state.ErrNotFound) {
			return preparedBatchEvent{}, batchEventAcceptanceFailure(lookupErr)
		}
	}
	if problem, retryable := s.batchEventSchemaProblem(ctx, acct.ID, req); problem != nil {
		return preparedBatchEvent{}, batchPublishFailure("rejected", retryable, problem)
	}
	if !api.WorkflowEventEnvelopeFits(payload) {
		return preparedBatchEvent{}, batchPublishFailure("rejected", false, api.ErrWorkflowEventEnvelopeTooLarge())
	}
	return preparedBatchEvent{envelope: envelope, payload: payload}, api.PublishEventBatchResult{}
}

func (s *Service) batchEventSchemaProblem(ctx context.Context, accountID string, req api.PublishEventRequest) (*api.Problem, bool) {
	registry, ok := s.Host.Store().(state.EventSchemaStore)
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
