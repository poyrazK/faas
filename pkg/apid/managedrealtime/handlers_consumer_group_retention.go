package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) RealtimeConsumerGroupDeadRetention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupRetentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("dead-letter retention unavailable"))
		return
	}
	var result state.RealtimeConsumerGroupRetentionPolicy
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupRetention(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	} else {
		var req struct {
			RetentionSeconds *int `json:"retention_seconds"`
		}
		if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil || req.RetentionSeconds == nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid retention policy"))
			return
		}
		result, err = store.SetRealtimeConsumerGroupRetention(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), *req.RetentionSeconds)
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.Host.EmitAudit(r.Context(), "realtime.consumer_group_retention_configured", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group"), "retention_seconds": result.RetentionSeconds})
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
