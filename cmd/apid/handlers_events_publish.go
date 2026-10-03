package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

// publishEvent handles POST /v1/events:publish. It persists the canonical
// envelope and wakes schedd's content-based fanout worker; the database
// trigger creates durable fanout work with the events row.
func (s *server) publishEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.PublishEventRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if req.AccountID != "" && req.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Forbidden", "account_id must match the authenticated account"))
		return
	}

	var occurredAt time.Time
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (events.Envelope{
		ID:              req.ID,
		Source:          req.Source,
		Type:            req.Type,
		Time:            occurredAt,
		DataContentType: req.DataContentType,
		Data:            req.Data,
		AccountID:       req.AccountID,
		SchemaVersion:   req.SchemaVersion,
	}).Normalize(acct.ID, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		api.WriteProblem(w, api.ErrValidation("gregale.* sources are reserved for platform events"))
		return
	}
	if !s.validateEventSchemaForIngress(w, r, acct.ID, envelope) {
		return
	}
	traceHeaders := pkgtrace.InjectHeaders(r.Context())
	envelope.Traceparent = traceHeaders["traceparent"]
	envelope.Tracestate = traceHeaders["tracestate"]
	envelope.Baggage = traceHeaders["baggage"]

	payload, err := json.Marshal(envelope)
	if err != nil {
		s.log.Error("marshal published event failed", "event_id", envelope.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	if err := s.store.AppendEvent(r.Context(), "apid", "event.published", &acct.ID, payload); err != nil {
		var pgErr *pgconn.PgError
		if errors.Is(err, state.ErrConflict) ||
			(errors.As(err, &pgErr) && pgErr.ConstraintName == "event_fanout_identity_uniq") {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Event identity conflict", "source and id already identify an event with different type, schema version, or data"))
			return
		}
		s.log.Error("record published event failed", "event_id", envelope.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to record event"))
		return
	}
	// LISTEN is a low-latency hint. The scheduler claims the transactional
	// outbox row even when this notification is missed or the payload is large.
	_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")

	writeJSON(w, http.StatusAccepted, api.PublishEventResponse{
		ID:         envelope.ID,
		AcceptedAt: time.Now().UTC(),
		AccountID:  acct.ID,
	})
}

func (s *server) validateEventSchemaForIngress(w http.ResponseWriter, r *http.Request, accountID string, envelope events.Envelope) bool {
	registry, ok := s.store.(state.EventSchemaStore)
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
	s.log.Error("event schema lookup failed", "source", envelope.Source, "type", envelope.Type, "err", err)
	api.WriteProblem(w, api.ErrCapacity("failed to validate event schema"))
	return false
}
