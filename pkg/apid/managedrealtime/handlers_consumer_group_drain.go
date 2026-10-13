package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) DrainRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	result, ok := s.startRealtimeGroupDrain(w, r, ep.ID, r.PathValue("channel"), r.PathValue("group"))
	if !ok {
		return
	}
	s.Host.EmitAudit(r.Context(), "realtime.consumer_group_drain", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group")})
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) RealtimeWorkerDrain(w http.ResponseWriter, r *http.Request) {
	g, _, ok := s.loadRealtimeWorker(w, r, "drain")
	if !ok {
		return
	}
	r = r.WithContext(state.WithRealtimeWorkerGrant(r.Context(), g, "drain"))
	result, ok := s.startRealtimeGroupDrain(w, r, g.EndpointID, g.Channel, g.Group)
	if !ok {
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, "drain")
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) startRealtimeGroupDrain(w http.ResponseWriter, r *http.Request, ep, ch, group string) (state.RealtimeConsumerGroupDrainState, bool) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupDrainStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("group drain controls unavailable"))
		return state.RealtimeConsumerGroupDrainState{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	result, err := store.DrainRealtimeConsumerGroup(r.Context(), ep, ch, group)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return result, false
	}
	return result, true
}
