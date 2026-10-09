package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func (s *server) publishAppEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecoveryRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.AppPublishEventRequest
	if err := decodeJSONSized(r, &req, api.AppEventPublishBodyMaxBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "app_event_publish_too_large", "Event too large", "publication exceeds the body byte limit"))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid event JSON"))
		}
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("application identity"))
		return
	}
	source := "app." + appID.String()
	digest := sha256.Sum256([]byte(req.Key))
	id := "key." + hex.EncodeToString(digest[:])
	envelope, err := normalizePublishRequest(api.PublishEventRequest{ID: id, Source: source, Type: req.Type, Time: req.Time, Data: req.Data, SchemaVersion: req.SchemaVersion}, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	trace := pkgtrace.InjectHeaders(ctx)
	envelope.Traceparent, envelope.Tracestate, envelope.Baggage = trace["traceparent"], trace["tracestate"], trace["baggage"]
	payload, err := json.Marshal(envelope)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid event data"))
		return
	}
	lookup, ok := s.store.(state.PublishedEventLookupStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acceptance lookup"))
		return
	}
	store, ok := s.store.(state.PublishedEventAcceptanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event acceptance store"))
		return
	}
	accepted, err := lookup.LookupPublishedEventAcceptance(ctx, acct.ID, payload)
	if errors.Is(err, state.ErrNotFound) {
		if !s.validateEventSchemaForIngress(w, r, acct.ID, envelope) {
			return
		}
		accepted, err = store.AcceptPublishedEvent(ctx, "apid", acct.ID, payload)
	}
	if err != nil {
		if writeEventStorageCapacity(w, err) {
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Producer key conflict", "key already identifies different type, schema version, or data for this application"))
			return
		}
		// A commit can succeed even if its response is lost. Keep the key unchanged.
		api.WriteProblem(w, api.ErrCapacity("event acceptance could not be confirmed; retry the same application, key, and content"))
		return
	}
	if !accepted.Duplicate {
		_ = s.notif.Notify(ctx, db.NotifyEventPublished, "1")
	}
	receipt := api.PublishEventResponse{ID: id, AccountID: acct.ID, AcceptedAt: accepted.AcceptedAt, ReceiptURL: eventReceiptURL(source, id)}
	w.Header().Set("Location", receipt.ReceiptURL)
	writeJSON(w, http.StatusAccepted, api.AppPublishEventResponse{AppID: appID.String(), Source: source, Duplicate: accepted.Duplicate, Receipt: receipt})
}
