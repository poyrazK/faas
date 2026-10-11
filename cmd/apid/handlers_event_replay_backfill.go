package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	req, problem := decodeEventReplayBackfillRequest(r)
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
	store, ok := s.store.(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	job, err := store.CreateEventReplayBackfill(ctx, acct.ID, state.EventReplayBackfillQuery{AppID: app.ID, SubscriptionID: subscriptionID.String(), EventReplayBackfillRequest: req})
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	if req.AllowExpired {
		s.audit.Emit(ctx, "event.subscription.delivery_age.overridden", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": subscriptionID.String(), "backfill_job_id": job.ID})
	}
	w.Header().Set("Location", "/v1/event-replays/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) createWorkflowEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	var req api.WorkflowEventReplayBackfillRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	job, err := store.CreateWorkflowEventReplayBackfill(ctx, acct.ID, state.WorkflowEventReplayBackfillQuery{AppID: app.ID, WorkflowEventReplayBackfillRequest: req})
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	w.Header().Set("Location", "/v1/event-replays/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) getEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	store, ok := s.store.(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	job, err := store.GetEventReplayBackfill(ctx, acct.ID, r.PathValue("jobID"))
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) listEventReplayBackfillItems(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	query, problem := parseEventReplayBackfillItemsQuery(r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	result, err := store.ListEventReplayBackfillItems(ctx, acct.ID, r.PathValue("jobID"), query)
	if err != nil {
		problem := eventReplayBackfillItemsProblem(err, ctx.Err())
		if problem.Status >= http.StatusInternalServerError {
			s.log.ErrorContext(ctx, "read event replay backfill items", "err", err)
		}
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) retryFailedEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	var req api.EventReplayBackfillRetryRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	limit := 0
	if req.Limit != nil {
		limit = *req.Limit
	}
	if req.Limit != nil && (limit < 1 || limit > state.EventReplayBackfillRetryMax) {
		api.WriteProblem(w, api.ErrValidation("limit must be between 1 and 100 when supplied"))
		return
	}
	store, ok := s.store.(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	result, err := store.RetryFailedEventReplayBackfill(ctx, acct.ID, r.PathValue("jobID"), limit)
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeEventReplayBackfillRequest(r *http.Request) (api.EventReplayBackfillRequest, *api.Problem) {
	var req api.EventReplayBackfillRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		return req, api.ErrValidation(err.Error())
	}
	if err := req.Validate(); err != nil {
		return req, api.ErrValidation(err.Error())
	}
	return req, nil
}

func parseEventReplayBackfillItemsQuery(values url.Values) (api.EventReplayBackfillItemsQuery, *api.Problem) {
	query := api.EventReplayBackfillItemsQuery{State: values.Get("state"), After: values.Get("after")}
	for key, entries := range values {
		if (key != "state" && key != "after" && key != "limit") || len(entries) != 1 {
			return query, api.ErrValidation("only one state, after and limit parameter may be supplied")
		}
	}
	if values.Has("state") && query.State == "" {
		return query, api.ErrValidation("state must be a supported backfill item state")
	}
	if values.Has("after") && query.After == "" {
		return query, api.ErrValidation("after must be a valid continuation cursor")
	}
	switch query.State {
	case "", "pending", "processing", "enqueued", "filtered", "failed", "skipped_captured", "skipped_unknown", "skipped_existing", "skipped_unsettled":
	default:
		return query, api.ErrValidation("state must be a supported backfill item state")
	}
	problem, limit := api.ParseLimit(values.Get("limit"), api.EventReplayBackfillItemsPageDefault, api.EventReplayBackfillItemsPageMax, "backfill items")
	if problem != nil {
		return query, problem
	}
	query.Limit = limit
	return query, nil
}

func eventReplayBackfillItemsProblem(err, contextErr error) *api.Problem {
	if contextErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return api.NewProblem(http.StatusServiceUnavailable, "event_replay_backfill_read_timeout", "Replay item read timed out", "Reduce the page limit and retry the read.")
	}
	return eventReplayBackfillProblem(err, nil)
}

func eventReplayBackfillProblem(err, contextErr error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event replay not found", "No replay job or current event subscription is available for this account.")
	case errors.Is(err, state.ErrEventReplayBackfillQuery):
		return api.ErrValidation(err.Error())
	case errors.Is(err, state.ErrEventReplayBackfillRange):
		return api.ErrValidation("backfill range cannot exceed the 30-day settled-receipt retention window")
	case errors.Is(err, state.ErrEventReplayBackfillDisabled):
		return api.NewProblem(http.StatusConflict, "event_replay_backfill_disabled", "Subscription disabled", err.Error())
	case errors.Is(err, state.ErrEventReplayBackfillUnsupported):
		return api.NewProblem(http.StatusConflict, "event_replay_backfill_unsupported", "Subscription unsupported", err.Error())
	case errors.Is(err, state.ErrEventReplayBackfillQuota):
		return api.NewProblem(http.StatusTooManyRequests, "event_replay_backfill_limit", "Active backfill limit reached", err.Error())
	case errors.Is(err, state.ErrEventReplayBackfillState):
		return api.NewProblem(http.StatusConflict, "event_replay_backfill_state", "Replay job is not retryable", err.Error())
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "event_replay_backfill_conflict", "Replay job changed", err.Error())
	case contextErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return api.NewProblem(http.StatusServiceUnavailable, "event_replay_backfill_timeout", "Replay request timed out", "Retry the request with the same idempotency key.")
	default:
		return api.ErrCapacity("failed to operate event replay backfill")
	}
}
