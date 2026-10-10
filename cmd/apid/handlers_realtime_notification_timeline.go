package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func (s *server) managedRealtimeNotificationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "notification timeline preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	principal := r.URL.Query().Get("principal")
	if api.ValidateRealtimePrincipal(principal) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal"))
		return
	}
	before := int64(0)
	if value := r.URL.Query().Get("before"); value != "" {
		var err error
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before < 0 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("before must be a nonnegative timeline ID"))
			return
		}
	}
	store, ok := s.store.(state.ManagedRealtimeNotificationTimelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("notification timeline unavailable"))
		return
	}
	events, err := store.ListManagedRealtimeNotificationTimeline(r.Context(), ep.ID, principal, r.PathValue("message_id"), before)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}
