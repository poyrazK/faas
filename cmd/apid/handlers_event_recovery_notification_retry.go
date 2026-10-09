package main

import (
	"net/http"
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
