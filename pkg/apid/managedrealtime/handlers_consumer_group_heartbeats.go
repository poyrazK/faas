package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) RealtimeConsumerGroupHeartbeats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	s.writeRealtimeWorkerHeartbeat(w, r, ep.ID, r.PathValue("channel"), r.PathValue("group"))
}
func (s *Service) RealtimeWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	g, _, ok := s.loadRealtimeWorker(w, r, "heartbeat")
	if !ok {
		return
	}
	r = r.WithContext(state.WithRealtimeWorkerGrant(r.Context(), g, "heartbeat"))
	s.writeRealtimeWorkerHeartbeat(w, r, g.EndpointID, g.Channel, g.Group)
}
func (s *Service) writeRealtimeWorkerHeartbeat(w http.ResponseWriter, r *http.Request, ep, ch, group string) {
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupHeartbeatStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("worker heartbeats unavailable"))
		return
	}
	var result any
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupWorkerHealth(r.Context(), ep, ch, group)
	} else {
		var req state.RealtimeWorkerHeartbeatRequest
		if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker heartbeat"))
			return
		}
		result, err = store.HeartbeatRealtimeConsumerGroup(r.Context(), ep, ch, group, req)
	}
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
