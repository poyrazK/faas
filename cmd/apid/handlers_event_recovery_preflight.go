package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) getEventRecoveryPreflight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	if len(r.URL.Query()) != 0 {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.store.(state.EventRecoveryPreflightStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery preflight"))
		return
	}
	out, err := store.GetEventRecoveryPreflight(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
