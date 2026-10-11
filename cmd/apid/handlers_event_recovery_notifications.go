package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEventRecoveryNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if len(r.URL.Query()) != 0 {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery notifications"))
		return
	}
	out, err := store.GetEventRecoveryNotifications(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
