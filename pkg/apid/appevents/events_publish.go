package appevents

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

// publishEvent handles POST /v1/events:publish. It persists the canonical
// envelope and wakes schedd's content-based fanout worker; the database
// trigger creates durable fanout work with the events row.
func (s *Service) PublishEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PublishEventRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, eventDecodeProblem(r, err))
		return
	}
	if req.AccountID != "" && req.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Forbidden", "account_id must match the authenticated account"))
		return
	}

	envelope, err := NormalizePublishRequest(req, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		api.WriteProblem(w, api.ErrValidation("gregale.* sources are reserved for platform events"))
		return
	}
	if !s.ValidateEventSchemaForIngress(w, r, acct.ID, envelope) {
		return
	}
	producer, problem := s.accountProducer(r.Context(), acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	envelope.Producer = producer
	traceHeaders := pkgtrace.InjectHeaders(r.Context())
	envelope.Traceparent = traceHeaders["traceparent"]
	envelope.Tracestate = traceHeaders["tracestate"]
	envelope.Baggage = traceHeaders["baggage"]

	s.persistPublishedEvent(w, r, acct, envelope)
}

func NormalizePublishRequest(req api.PublishEventRequest, accountID string) (events.Envelope, error) {
	var occurredAt time.Time
	if req.Time != nil {
		occurredAt = *req.Time
	}
	return (events.Envelope{ID: req.ID, Source: req.Source, Type: req.Type, Time: occurredAt,
		DataContentType: req.DataContentType, Data: req.Data, AccountID: req.AccountID,
		SchemaVersion: req.SchemaVersion}).Normalize(accountID, time.Now().UTC())
}

func (s *Service) persistPublishedEvent(w http.ResponseWriter, r *http.Request, acct state.Account, envelope events.Envelope) {
	payload, err := json.Marshal(envelope)
	if err != nil {
		s.Host.Logger().Error("marshal published event failed", "event_id", logsanitize.Field(envelope.ID), "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	if err := s.Host.Store().AppendEvent(r.Context(), "apid", "event.published", &acct.ID, payload); err != nil {
		if WriteEventStorageCapacity(w, err) {
			return
		}
		var pgErr *pgconn.PgError
		if errors.Is(err, state.ErrConflict) ||
			(errors.As(err, &pgErr) && pgErr.ConstraintName == "event_fanout_identity_uniq") {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Event identity conflict", "source and id already identify an event with different type, schema version, or data"))
			return
		}
		s.Host.Logger().Error("record published event failed", "event_id", logsanitize.Field(envelope.ID), "err", logsanitize.FieldAny(err))
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	// LISTEN is a low-latency hint. The scheduler claims the transactional
	// outbox row even when this notification is missed or the payload is large.
	_ = s.Host.Notify(r.Context(), db.NotifyEventPublished, "1")

	s.writePublishedEventReceipt(w, r, acct, envelope)
}

func (s *Service) writePublishedEventReceipt(w http.ResponseWriter, r *http.Request, acct state.Account, envelope events.Envelope) {
	store, ok := s.Host.Store().(state.EventReceiptAcceptanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt acceptance store"))
		return
	}
	acceptedAt, err := store.EventReceiptAcceptedAt(r.Context(), acct.ID, envelope.Source, envelope.ID)
	if err != nil {
		s.Host.Logger().ErrorContext(r.Context(), "read published event acceptance", "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to read event acceptance; retry the same source and id"))
		return
	}
	w.Header().Set("Location", EventReceiptURL(envelope.Source, envelope.ID))
	httpkit.WriteJSON(w, http.StatusAccepted, api.PublishEventResponse{
		ReceiptURL: EventReceiptURL(envelope.Source, envelope.ID),
		ID:         envelope.ID,
		AcceptedAt: acceptedAt,
		AccountID:  acct.ID,
	})
}

func (s *Service) ValidateEventSchemaForIngress(w http.ResponseWriter, r *http.Request, accountID string, envelope events.Envelope) bool {
	registry, ok := s.Host.Store().(state.EventSchemaStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event schema registry"))
		return false
	}
	err := state.ValidatePublishedEventSchema(r.Context(), registry, accountID, envelope.Source, envelope.Type, envelope.SchemaVersion, envelope.Data)
	if err == nil {
		return true
	}
	if errors.Is(err, state.ErrEventSchemaRequired) || errors.Is(err, state.ErrEventSchemaUnknown) || errors.Is(err, state.ErrEventDataInvalid) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Event schema validation failed", err.Error()))
		return false
	}
	s.Host.Logger().Error("event schema lookup failed", "source", logsanitize.Field(envelope.Source), "type", logsanitize.Field(envelope.Type), "err", logsanitize.FieldAny(err))
	api.WriteProblem(w, api.ErrCapacity("failed to validate event schema"))
	return false
}
