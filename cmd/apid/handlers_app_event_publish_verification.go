package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) verifyAppEventPublication(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecoveryRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.ErrValidation("verification does not accept query parameters"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.AppPublishEventRequest
	if err := decodeJSONSized(r, &req, api.AppEventPublishBodyMaxBytes); err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, "app_event_verification_too_large", "Verification too large", "original event exceeds the body byte limit"))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid event JSON"))
		}
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	appID, source, id, err := appProducerEventIdentity(app.ID, req.Key)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("application identity"))
		return
	}
	envelope, err := normalizePublishRequest(api.PublishEventRequest{ID: id, Source: source, Type: req.Type, Time: req.Time, Data: req.Data, SchemaVersion: req.SchemaVersion}, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	// Today's schema registry is irrelevant to the already accepted content.
	payload, err := json.Marshal(envelope)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid event data"))
		return
	}
	store, ok := s.store.(state.PublishedEventComparisonStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event comparison store"))
		return
	}
	comparison, err := store.ComparePublishedEventAcceptance(ctx, acct.ID, payload)
	out := api.AppEventPublicationVerification{AppID: appID, Source: source, EventID: id, ReceiptURL: eventReceiptURL(source, id), ObservedAt: time.Now().UTC()}
	if errors.Is(err, state.ErrNotFound) {
		out.Status, out.Reason = "unavailable", "not_retained_or_not_observed"
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	out.Status = "conflict"
	if comparison.Matches {
		out.Status = "match"
	}
	out.Receipt = &api.PublishEventResponse{ID: id, AccountID: acct.ID, AcceptedAt: comparison.AcceptedAt, ReceiptURL: out.ReceiptURL}
	writeJSON(w, http.StatusOK, out)
}
