package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeConsumerGroupRetryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupRetryPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("retry policy unavailable"))
		return
	}
	var result state.RealtimeConsumerGroupRetryStatus
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupRetryPolicy(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	} else {
		var req struct {
			Enabled               *bool    `json:"enabled"`
			BaseDelaySeconds      *int     `json:"base_delay_seconds"`
			MaxDelaySeconds       *int     `json:"max_delay_seconds"`
			Jitter                *bool    `json:"jitter"`
			PermanentFailureCodes []string `json:"permanent_failure_codes"`
		}
		if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil || req.Enabled == nil || req.BaseDelaySeconds == nil || req.MaxDelaySeconds == nil || req.Jitter == nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("retry policy requires enabled, base_delay_seconds, max_delay_seconds and jitter"))
			return
		}
		result, err = store.SetRealtimeConsumerGroupRetryPolicy(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), state.RealtimeConsumerGroupRetryPolicy{Enabled: *req.Enabled, BaseDelaySeconds: *req.BaseDelaySeconds, MaxDelaySeconds: *req.MaxDelaySeconds, Jitter: *req.Jitter, PermanentFailureCodes: req.PermanentFailureCodes})
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.audit.Emit(r.Context(), "realtime.consumer_group_retry_policy_configured", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group"), "enabled": result.Enabled})
	}
	writeJSON(w, http.StatusOK, result)
}
