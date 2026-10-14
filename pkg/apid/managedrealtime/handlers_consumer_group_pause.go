package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) PauseRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setRealtimeConsumerGroupPaused(w, r, acct, true)
}
func (s *Service) ResumeRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setRealtimeConsumerGroupPaused(w, r, acct, false)
}
func (s *Service) setRealtimeConsumerGroupPaused(w http.ResponseWriter, r *http.Request, acct state.Account, paused bool) {
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
	s.Host.EmitAudit(r.Context(), "realtime.consumer_group_"+action, &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group")})
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) RealtimeWorkerPause(w http.ResponseWriter, r *http.Request) {
	s.setRealtimeWorkerGroupPaused(w, r, true)
}
func (s *Service) RealtimeWorkerResume(w http.ResponseWriter, r *http.Request) {
	s.setRealtimeWorkerGroupPaused(w, r, false)
}
func (s *Service) setRealtimeWorkerGroupPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	action := "resume"
	if paused {
		action = "pause"
	}
	g, _, ok := s.loadRealtimeWorker(w, r, action)
	if !ok {
		return
	}
	r = r.WithContext(state.WithRealtimeWorkerGrant(r.Context(), g, action))
	result, ok := s.changeRealtimeGroupPause(w, r, g.EndpointID, g.Channel, g.Group, paused)
	if !ok {
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, action)
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) changeRealtimeGroupPause(w http.ResponseWriter, r *http.Request, ep, ch, group string, paused bool) (state.RealtimeConsumerGroupPauseState, bool) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupPauseStore)
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
