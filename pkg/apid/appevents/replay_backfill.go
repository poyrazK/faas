package appevents

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) CreateEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
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
		s.Host.EmitAudit(ctx, "event.subscription.delivery_age.overridden", &acct.ID, map[string]any{"app_id": app.ID, "subscription_id": subscriptionID.String(), "backfill_job_id": job.ID})
	}
	w.Header().Set("Location", "/v1/event-replays/"+job.ID)
	httpkit.WriteJSON(w, http.StatusAccepted, job)
}

func (s *Service) CreateWorkflowEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	var req api.WorkflowEventReplayBackfillRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
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
	httpkit.WriteJSON(w, http.StatusAccepted, job)
}

func (s *Service) GetEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	job, err := s.visibleEventReplayBackfill(ctx, store, acct, r.PathValue("jobID"))
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, job)
}

// visibleEventReplayBackfill reads a replay job and answers ErrNotFound for one
// whose app the caller may not see (ADR-20261025211616).
func (s *Service) visibleEventReplayBackfill(ctx context.Context, store state.EventReplayBackfillStore, acct state.Account, jobID string) (api.EventReplayBackfillJobResponse, error) {
	job, err := store.GetEventReplayBackfill(ctx, acct.ID, jobID)
	if err != nil || job.AppSlug == "" {
		return job, err
	}
	access, err := s.eventAccess(ctx, acct)
	if err != nil {
		return job, err
	}
	visible, err := access.appSlug(job.AppSlug)
	if err == nil && !visible {
		err = state.ErrNotFound
	}
	return job, err
}

func (s *Service) ListEventReplayBackfillItems(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	query, problem := parseEventReplayBackfillItemsQuery(r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	_, err := s.visibleEventReplayBackfill(ctx, store, acct, r.PathValue("jobID"))
	var result api.EventReplayBackfillItemsResponse
	if err == nil {
		result, err = store.ListEventReplayBackfillItems(ctx, acct.ID, r.PathValue("jobID"), query)
	}
	if err != nil {
		problem := eventReplayBackfillItemsProblem(err, ctx.Err())
		if problem.Status >= http.StatusInternalServerError {
			s.Host.Logger().ErrorContext(ctx, "read event replay backfill items", "err", err)
		}
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) RetryFailedEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	var req api.EventReplayBackfillRetryRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
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
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	if _, err := s.visibleEventReplayBackfill(ctx, store, acct, r.PathValue("jobID")); err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	result, err := store.RetryFailedEventReplayBackfill(ctx, acct.ID, r.PathValue("jobID"), limit)
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}

// CancelEventReplayBackfill ends a running backfill as cancelled
// (ADR-20261028095548). Cancelling an ended job returns it unchanged.
func (s *Service) CancelEventReplayBackfill(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayBackfillRequestTimeout)
	defer cancel()
	store, ok := s.Host.Store().(state.EventReplayBackfillStore)
	lifecycle, canEnd := s.Host.Store().(state.EventReplayBackfillLifecycleStore)
	if !ok || !canEnd {
		api.WriteProblem(w, api.ErrInternal("event replay backfill store"))
		return
	}
	if _, err := s.visibleEventReplayBackfill(ctx, store, acct, r.PathValue("jobID")); err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	job, err := lifecycle.CancelEventReplayBackfill(ctx, acct.ID, r.PathValue("jobID"))
	if err != nil {
		api.WriteProblem(w, eventReplayBackfillProblem(err, ctx.Err()))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, job)
}

func decodeEventReplayBackfillRequest(r *http.Request) (api.EventReplayBackfillRequest, *api.Problem) {
	var req api.EventReplayBackfillRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
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
	case "", "pending", "processing", "enqueued", "filtered", "failed", "skipped_captured", "skipped_unknown", "skipped_existing", "skipped_unsettled", "cancelled":
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

// eventReplayBackfillRangeProblem names the plan's settled-receipt window
// and the requested range, in seconds.
func eventReplayBackfillRangeProblem(err error) *api.Problem {
	var rangeErr *state.EventReplayBackfillRangeError
	if !errors.As(err, &rangeErr) {
		return api.ErrValidation("backfill range cannot exceed the plan's settled-receipt retention window")
	}
	return api.ErrEventReplayBackfillRange(int64(rangeErr.Limit/time.Second), int64((rangeErr.Range+time.Second-1)/time.Second))
}

func eventReplayBackfillProblem(err, contextErr error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event replay not found", "No replay job or current event subscription is available for this account.")
	case errors.Is(err, state.ErrEventReplayBackfillQuery):
		return api.ErrValidation(err.Error())
	case errors.Is(err, state.ErrEventReplayBackfillRange):
		return eventReplayBackfillRangeProblem(err)
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
