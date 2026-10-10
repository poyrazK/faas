package main

import (
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewEventRecoveryNotificationRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if r.URL.RawQuery != "" {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationRetryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification retry preview"))
		return
	}
	out, err := store.PreviewEventRecoveryNotificationRetry(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) retryEventRecoveryNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if r.URL.RawQuery != "" {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	var req api.EventRecoveryNotificationRetryRequest
	if err := decodeJSONSized(r, &req, api.EventRecoveryNotificationRetryBodyMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationRetryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification retry"))
		return
	}
	out, err := store.RetryEventRecoveryNotifications(r.Context(), acct.ID, r.PathValue("jobID"), req, time.Now().UTC())
	if err == nil {
		s.audit.Emit(r.Context(), "app.event_recovery_notification_retry_decided", &acct.ID, map[string]any{"job_id": out.JobID, "app_id": out.AppID, "request_id": out.RequestID, "decided_at": out.DecidedAt, "results": out.Results})
	}
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *server) listEventRecoveryNotificationRetryHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	statuses, queryErr := eventRecoveryNotificationRetryHistoryStatus(r)
	if queryErr != nil {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationRetryHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification retry history"))
		return
	}
	out, err := store.GetEventRecoveryNotificationRetryHistory(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	if err == nil {
		out.ApplyStatusFilter(statuses)
	}
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) getEventRecoveryNotificationRetryDecision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if r.URL.RawQuery != "" {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationRetryHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification retry history"))
		return
	}
	out, err := store.GetEventRecoveryNotificationRetryDecision(r.Context(), acct.ID, r.PathValue("jobID"), r.PathValue("requestID"), time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func eventRecoveryNotificationRetryHistoryStatus(r *http.Request) ([]string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, state.ErrEventRecoveryQuery
	}
	if len(query) == 0 {
		return nil, nil
	}
	values, ok := query["status"]
	if !ok || len(query) != 1 || len(values) != 1 {
		return nil, state.ErrEventRecoveryQuery
	}
	statuses, err := api.ParseEventRecoveryNotificationRetryHistoryStatus(values[0])
	if err != nil {
		return nil, state.ErrEventRecoveryQuery
	}
	return statuses, nil
}
