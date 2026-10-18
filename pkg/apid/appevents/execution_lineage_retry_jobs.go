package appevents

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) lineageRetryJobTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.EventExecutionLineageRetryJobsStore, string, bool) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, "", false
	}
	store, ok := s.Host.Store().(state.EventExecutionLineageRetryJobsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("lineage retry jobs unavailable"))
		return nil, "", false
	}
	return store, app.ID, true
}
func (s *Service) PreviewEventExecutionLineageRetries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	after, limit := int64(0), 0
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeLineageCaseError(w, state.ErrInvalidArgument)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			writeLineageCaseError(w, state.ErrInvalidArgument)
			return
		}
	}
	out, err := store.PreviewEventExecutionLineageRetries(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.URL.Query().Get("reason"), after, limit)
	if !writeLineageCaseError(w, err) {
		httpkit.WriteJSON(w, 200, out)
	}
}
func (s *Service) CreateEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateEventExecutionLineageRetryJobRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.EventExecutionLineageRetryJobBodyMaxBytes) {
		return
	}
	out, err := store.CreateEventExecutionLineageRetryJob(ctx, acct.ID, app, r.PathValue("subscriptionID"), req)
	if !writeLineageCaseError(w, err) {
		status := http.StatusAccepted
		if out.Status != "pending" {
			status = http.StatusOK
		}
		httpkit.WriteJSON(w, status, out)
	}
}
func (s *Service) GetEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetEventExecutionLineageRetryJob(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.PathValue("requestID"))
	if !writeLineageCaseError(w, err) {
		httpkit.WriteJSON(w, 200, out)
	}
}

func writeLineageRetryJobCancelError(w http.ResponseWriter, err error) bool {
	if writeLineageRetryControlReceiptError(w, err) {
		return true
	}
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_lineage_retry_job_changed", "Lineage retry job changed", "refresh the job and use its original creation timestamp"))
		return true
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(504, api.CodeInternal, "Lineage cancellation timed out", "repeat cancellation with the same job UUID, original creation timestamp and command UUID if supplied"))
		return true
	default:
		return writeLineageCaseError(w, err)
	}
}
func (s *Service) CancelEventExecutionLineageRetryJob(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	store, app, ok := s.lineageRetryJobTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.CancelEventExecutionLineageRetryJobRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.EventExecutionLineageRetryJobBodyMaxBytes) {
		return
	}
	ctx = lineageControlActorContext(ctx, r)
	out, err := store.CancelEventExecutionLineageRetryJob(ctx, acct.ID, app, r.PathValue("subscriptionID"), r.PathValue("requestID"), req)
	if !writeLineageRetryJobCancelError(w, err) {
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
