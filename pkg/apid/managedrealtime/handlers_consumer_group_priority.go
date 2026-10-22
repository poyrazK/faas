package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) RealtimeConsumerGroupPriority(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupPriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("priority observation unavailable"))
		return
	}
	result, err := store.ReadRealtimeConsumerGroupPriority(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
