package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func (s *Service) RealtimeConsumerGroupDeliveryHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupDeliveryHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("delivery attempt history unavailable"))
		return
	}
	after, limit, ok := realtimeDeliveryHistoryPage(r)
	if !ok {
		api.WriteProblem(w, api.ErrRealtimeInvalid("after_id must occur once; limit must be from 1 to 100"))
		return
	}
	result, err := store.ReadRealtimeConsumerGroupDeliveryHistory(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), r.PathValue("sequence"), after, limit)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func realtimeDeliveryHistoryPage(r *http.Request) (string, int, bool) {
	after := "0"
	limit := api.RealtimeInspectorSampleMax
	if values, exists := r.URL.Query()["after_id"]; exists {
		if len(values) != 1 {
			return "", 0, false
		}
		after = values[0]
	}
	if values, exists := r.URL.Query()["limit"]; exists {
		if len(values) != 1 {
			return "", 0, false
		}
		var err error
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > api.RealtimeConsumerGroupDeliveryHistoryPageMax {
			return "", 0, false
		}
	}
	return after, limit, true
}
