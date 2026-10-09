package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getAppEventPublishStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecoveryRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid status query encoding"))
		return
	}
	for key, entries := range values {
		if (key != "key" && key != "after" && key != "limit") || len(entries) != 1 || entries[0] == "" {
			api.WriteProblem(w, api.ErrValidation("status accepts one nonempty key, after and limit value only"))
			return
		}
	}
	query := api.AppEventPublishStatusQuery{Key: values.Get("key"), After: values.Get("after")}
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 {
			api.WriteProblem(w, api.ErrValidation("invalid recipient limit"))
			return
		}
	}
	if err := query.Normalize(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	appID, source, id, err := appProducerEventIdentity(app.ID, query.Key)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("application identity"))
		return
	}
	cursor, err := decodeEventReceiptCursor(query.After, acct.ID, source, id)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt store"))
		return
	}
	retained, err := store.EventReceipt(ctx, acct.ID, source, id, cursor, query.Limit)
	out := api.AppEventPublishStatusResponse{AppID: appID, Source: source, EventID: id, ReceiptURL: eventReceiptURL(source, id), ObservedAt: time.Now().UTC()}
	if errors.Is(err, state.ErrNotFound) {
		out.Status, out.Reason = "unavailable", "not_retained_or_not_observed"
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	evidence := eventReceiptResponse(acct.ID, retained)
	out.Evidence = &evidence
	out.Receipt = &api.PublishEventResponse{ID: id, AccountID: acct.ID, AcceptedAt: retained.AcceptedAt, ReceiptURL: out.ReceiptURL}
	out.Status = "accepted"
	if retained.RoutingSettledAt == nil {
		out.Status = "processing"
	}
	writeJSON(w, http.StatusOK, out)
}
