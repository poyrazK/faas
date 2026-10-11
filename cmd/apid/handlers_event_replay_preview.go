package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewEventReplay(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayPreviewReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	options, problem := parseEventReplayPreview(r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	subscriptionID, err := uuid.Parse(r.PathValue("subscriptionID"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventReplayPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay preview store"))
		return
	}
	result, err := store.PreviewEventReplay(ctx, acct.ID, state.EventReplayPreviewQuery{AppID: app.ID, SubscriptionID: subscriptionID.String(), EventReplayPreviewOptions: options})
	if err != nil {
		problem := eventReplayPreviewProblem(err, ctx.Err())
		if problem.Status >= http.StatusInternalServerError {
			s.log.ErrorContext(ctx, "read retained event replay preview", "err", err)
		}
		api.WriteProblem(w, problem)
		return
	}
	for i := range result.Matches {
		result.Matches[i].ReceiptURL = eventReceiptURL(result.Matches[i].EventSource, result.Matches[i].EventID)
	}
	writeJSON(w, http.StatusOK, result)
}

func parseEventReplayPreview(values url.Values) (api.EventReplayPreviewOptions, *api.Problem) {
	options := api.EventReplayPreviewOptions{After: values.Get("after")}
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"from", &options.From}, {"until", &options.Until}} {
		parsed, err := time.Parse(time.RFC3339Nano, values.Get(field.name))
		if err != nil {
			return options, api.ErrValidation(field.name + " must be an RFC3339 acceptance timestamp")
		}
		*field.target = parsed.UTC()
	}
	problem, limit := api.ParseLimit(values.Get("limit"), api.EventReplayPreviewPageDefault, api.EventReplayPreviewPageMax, "retained envelopes")
	if problem != nil {
		return options, problem
	}
	options.Limit = limit
	if err := options.Validate(); err != nil {
		return options, api.ErrValidation(err.Error())
	}
	return options, nil
}

func eventReplayPreviewProblem(err, contextErr error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event subscription not found", "No current event subscription is available for this app.")
	case errors.Is(err, state.ErrEventReplayPreviewQuery):
		return api.ErrValidation(err.Error())
	case errors.Is(err, state.ErrEventReplayPreviewChanged):
		return api.NewProblem(http.StatusConflict, "event_replay_preview_changed", "Subscription changed", err.Error())
	case errors.Is(err, state.ErrEventReplayPreviewDisabled):
		return api.NewProblem(http.StatusConflict, "event_replay_preview_disabled", "Subscription disabled", err.Error())
	case errors.Is(err, state.ErrEventReplayPreviewUnsupported):
		return api.NewProblem(http.StatusConflict, "event_replay_preview_unsupported", "Subscription unsupported", err.Error())
	case contextErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return api.NewProblem(http.StatusServiceUnavailable, "event_replay_preview_read_timeout", "Replay preview read timed out", "Narrow the acceptance range or reduce the page limit and retry.")
	default:
		return api.ErrCapacity("failed to read retained event replay preview")
	}
}
