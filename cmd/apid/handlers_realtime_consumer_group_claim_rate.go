package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeConsumerGroupClaimRate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupClaimRateStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("claim rate policy unavailable"))
		return
	}
	var result state.RealtimeConsumerGroupClaimRateStatus
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupClaimRate(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	} else {
		var req struct {
			RatePerSecond *int `json:"rate_per_second"`
			Burst         *int `json:"burst"`
		}
		if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil || req.RatePerSecond == nil || req.Burst == nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("rate_per_second and burst are required"))
			return
		}
		result, err = store.SetRealtimeConsumerGroupClaimRate(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), state.RealtimeConsumerGroupClaimRatePolicy{RatePerSecond: *req.RatePerSecond, Burst: *req.Burst})
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.audit.Emit(r.Context(), "realtime.consumer_group_claim_rate_configured", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group"), "rate_per_second": result.RatePerSecond, "burst": result.Burst})
	}
	writeJSON(w, http.StatusOK, result)
}
