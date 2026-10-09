package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) eventRecoveryStore(w http.ResponseWriter, r *http.Request) (state.EventRecoveryStore, *http.Request, context.CancelFunc, bool) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecoveryRequestTimeout)
	store, ok := s.store.(state.EventRecoveryStore)
	if !ok {
		cancel()
		api.WriteProblem(w, api.ErrInternal("event recovery store"))
		return nil, r, func() {}, false
	}
	return store, recoveryActorRequest(r.WithContext(ctx)), cancel, true
}
func (s *server) writeEventRecovery(w http.ResponseWriter, r *http.Request, status int, out any, err error) {
	if err == nil {
		writeJSON(w, status, out)
		return
	}
	switch {
	case errors.Is(err, state.ErrEventRecoveryQuery):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	case errors.Is(err, state.ErrEventRecoveryRetryParent):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Parent recovery is not ready", err.Error()))
	case errors.Is(err, state.ErrEventRecoveryRequestConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Recovery request conflicts", err.Error()))
	case errors.Is(err, state.ErrEventRecoveryState):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Event recovery is no longer active", "completed, cancelled, and expired jobs cannot be paused, resumed, or rate-adjusted"))
	case errors.Is(err, state.ErrEventRecoveryNotificationRetryDecisionNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Retry decision not found", "the request identifier is not retained for this recovery job"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event recovery not found", "the recovery target does not belong to this account"))
	case errors.Is(err, state.ErrEventRecoveryNotificationRetryLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Notification retry limit reached", fmt.Sprintf("at most %d retry decisions per retained job; see /docs/event-driven", api.EventRecoveryNotificationRetryReceiptsMax)))
	case errors.Is(err, state.ErrEventRecoveryQuota):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Event recovery limit reached", fmt.Sprintf("at most %d active jobs per account; see /docs/event-driven", api.EventRecoveryActiveJobsMax)))
	case errors.Is(err, state.ErrEventRecoverySelection):
		api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("selection exceeds %d recipients; narrow the filters; see /docs/event-driven", api.EventRecoveryRecipientsMax)))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Event recovery timed out", "retry the request"))
	default:
		s.log.ErrorContext(r.Context(), "event recovery failed", "err", err)
		api.WriteProblem(w, api.ErrInternal("event recovery"))
	}
}
func decodeEventRecovery(r *http.Request) (api.EventRecoveryRequest, error) {
	var req api.EventRecoveryRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		return req, fmt.Errorf("%w: %w", state.ErrEventRecoveryQuery, err)
	}
	if err := req.Validate(); err != nil {
		return req, fmt.Errorf("%w: %w", state.ErrEventRecoveryQuery, err)
	}
	return req, nil
}
func (s *server) previewEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	req, err := decodeEventRecovery(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.PreviewEventRecovery(r.Context(), acct.ID, app.ID, req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) createEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	req, err := decodeEventRecovery(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.CreateEventRecovery(r.Context(), acct.ID, app.ID, req)
	if err == nil {
		w.Header().Set("Location", "/v1/event-recoveries/"+out.ID)
	}
	s.writeEventRecovery(w, r, http.StatusAccepted, out, err)
}
func (s *server) getEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	out, err := store.GetEventRecovery(r.Context(), acct.ID, r.PathValue("jobID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) cancelEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	r, err := recoveryControlRequest(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	out, err := store.CancelEventRecovery(r.Context(), acct.ID, r.PathValue("jobID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) listEventRecoveryItems(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	values := r.URL.Query()
	after := int64(0)
	for name, entries := range values {
		if (name != "after" && name != "limit") || len(entries) != 1 {
			s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
			return
		}
	}
	if values.Has("after") {
		var err error
		after, err = strconv.ParseInt(values.Get("after"), 10, 64)
		if err != nil || after < 0 {
			s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
			return
		}
	}
	problem, limit := api.ParseLimit(values.Get("limit"), api.EventRecoveryItemsPageMax, api.EventRecoveryItemsPageMax, "recovery items")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	out, err := store.ListEventRecoveryItems(r.Context(), acct.ID, r.PathValue("jobID"), after, limit)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *server) pauseEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	r, err := recoveryControlRequest(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	out, err := store.PauseEventRecovery(r.Context(), acct.ID, r.PathValue("jobID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) resumeEventRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	r, err := recoveryControlRequest(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	out, err := store.ResumeEventRecovery(r.Context(), acct.ID, r.PathValue("jobID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *server) setEventRecoveryRate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	var req api.EventRecoveryRateRequest
	if err := decodeJSONSized(r, &req, 4<<10); err != nil {
		s.writeEventRecovery(w, r, 0, nil, errors.Join(state.ErrEventRecoveryQuery, err))
		return
	}
	out, err := store.SetEventRecoveryRate(r.Context(), acct.ID, r.PathValue("jobID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
