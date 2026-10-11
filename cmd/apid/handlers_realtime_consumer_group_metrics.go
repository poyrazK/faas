package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeConsumerGroupMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupMetricsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("consumer group metrics unavailable"))
		return
	}
	result, err := store.ReadRealtimeConsumerGroupMetrics(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
