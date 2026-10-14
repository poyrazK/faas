package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func writeRealtimeCircuitProblem(w http.ResponseWriter, circuit *state.RealtimeConsumerGroupCircuitOpenError) {
	seconds := (circuit.RetryAfterMilliseconds + 999) / 1000
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	problem := api.NewProblem(http.StatusTooManyRequests, "realtime_group_circuit_open", "Consumer group circuit blocks claims", "wait for cooldown or the active probe; existing leases may settle")
	problem.RetryAfterSeconds = &seconds
	api.WriteProblem(w, problem)
}
func (s *Service) RealtimeConsumerGroupCircuit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupCircuitStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("circuit policy unavailable"))
		return
	}
	var result state.RealtimeConsumerGroupCircuitStatus
	var err error
	if r.Method == http.MethodGet {
		result, err = store.ReadRealtimeConsumerGroupCircuit(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	} else {
		var req struct {
			Enabled          *bool `json:"enabled"`
			FailureThreshold *int  `json:"failure_threshold"`
			CooldownSeconds  *int  `json:"cooldown_seconds"`
		}
		if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil || req.Enabled == nil || req.FailureThreshold == nil || req.CooldownSeconds == nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("enabled, failure_threshold and cooldown_seconds are required"))
			return
		}
		result, err = store.SetRealtimeConsumerGroupCircuit(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), state.RealtimeConsumerGroupCircuitPolicy{Enabled: *req.Enabled, FailureThreshold: *req.FailureThreshold, CooldownSeconds: *req.CooldownSeconds})
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.Host.EmitAudit(r.Context(), "realtime.consumer_group_circuit_configured", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group": r.PathValue("group"), "enabled": result.Enabled, "failure_threshold": result.FailureThreshold, "cooldown_seconds": result.CooldownSeconds})
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
