package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) pauseRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setRealtimeConsumerGroupPaused(w, r, acct, true)
}
func (s *server) resumeRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setRealtimeConsumerGroupPaused(w, r, acct, false)
}
func (s *server) setRealtimeConsumerGroupPaused(w http.ResponseWriter, r *http.Request, acct state.Account, paused bool) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	result, ok := s.changeRealtimeGroupPause(w, r, ep.ID, r.PathValue("channel"), r.PathValue("group"), paused)
	if !ok {
		return
	}
	action := "resume"
	if paused {
		action = "pause"
	}
	s.audit.Emit(r.Context(), "realtime.consumer_group_"+action, &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group")})
	writeJSON(w, http.StatusOK, result)
}
func (s *server) realtimeWorkerPause(w http.ResponseWriter, r *http.Request) {
	s.setRealtimeWorkerGroupPaused(w, r, true)
}
func (s *server) realtimeWorkerResume(w http.ResponseWriter, r *http.Request) {
	s.setRealtimeWorkerGroupPaused(w, r, false)
}
func (s *server) setRealtimeWorkerGroupPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	action := "resume"
	if paused {
		action = "pause"
	}
	r, g, _, ok := s.loadRealtimeWorker(w, r, action)
	if !ok {
		return
	}
	result, ok := s.changeRealtimeGroupPause(w, r, g.EndpointID, g.Channel, g.Group, paused)
	if !ok {
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, action)
	writeJSON(w, http.StatusOK, result)
}
func (s *server) changeRealtimeGroupPause(w http.ResponseWriter, r *http.Request, ep, ch, group string, paused bool) (state.RealtimeConsumerGroupPauseState, bool) {
	store, ok := s.store.(state.RealtimeConsumerGroupPauseStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("group pause controls unavailable"))
		return state.RealtimeConsumerGroupPauseState{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	result, err := store.SetRealtimeConsumerGroupPaused(r.Context(), ep, ch, group, paused)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return result, false
	}
	return result, true
}
