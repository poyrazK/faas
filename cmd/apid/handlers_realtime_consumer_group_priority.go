package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeConsumerGroupPriority(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupPriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("priority observation unavailable"))
		return
	}
	result, err := store.ReadRealtimeConsumerGroupPriority(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
