package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) RealtimeConsumerGroupAlert(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupAlertStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("group lag alerts unavailable"))
		return
	}
	var result state.RealtimeConsumerGroupAlert
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupAlert(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	} else {
		var config state.RealtimeConsumerGroupAlertConfig
		if httpkit.DecodeJSONSized(r, &config, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid lag alert configuration"))
			return
		}
		result, err = store.SetRealtimeConsumerGroupAlert(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), config)
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.Host.EmitAudit(r.Context(), "realtime.consumer_group_alert_configured", &acct.ID, map[string]any{"endpoint_id": ep.ID, "group": r.PathValue("group"), "channel": r.PathValue("channel")})
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
