package managedrealtime

import (
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) loadRealtimeConsumerGroups(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, state.RealtimeConsumerGroupStore, bool) {
	if s.previewUnavailable(w, acct.ID, s.HistoryPreviewEnabled, "consumer groups preview unavailable") {
		return state.ManagedRealtimeEndpoint{}, nil, false
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return ep, nil, false
	}
	if problem := validateManagedRealtimeChannel(r.PathValue("channel")); problem != nil {
		api.WriteProblem(w, problem)
		return ep, nil, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodDelete && !ep.Enabled {
		api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled"))
		return ep, nil, false
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("consumer groups require durable PostgreSQL storage"))
		return ep, nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return ep, store, true
}
func (s *Service) writeRealtimeConsumerGroupError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	var deduplicationFull *state.RealtimeConsumerGroupDeduplicationFullError
	if errors.As(err, &deduplicationFull) {
		writeRealtimeDeduplicationProblem(w, deduplicationFull)
		return
	}
	var circuit *state.RealtimeConsumerGroupCircuitOpenError
	if errors.As(err, &circuit) {
		writeRealtimeCircuitProblem(w, circuit)
		return
	}
	var rate *state.RealtimeConsumerGroupRateLimitError
	if errors.As(err, &rate) {
		seconds := (rate.RetryAfterMilliseconds + 999) / 1000
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		problem := api.NewProblem(http.StatusTooManyRequests, "realtime_group_rate_limited", "Consumer group claim rate reached", "wait before claiming more work; renewals and settlements remain available")
		problem.RetryAfterSeconds = &seconds
		api.WriteProblem(w, problem)
		return
	}
	switch {
	case errors.Is(err, state.ErrRealtimeConsumerGroupInvalid):
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid group, worker, sequence, claim token, idempotency key or retry policy"))
	case errors.Is(err, state.ErrRealtimeSettlementKeyConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "realtime_settlement_key_conflict", "Settlement key reused", "reuse the key only with the original settlement request"))
	case errors.Is(err, state.ErrRealtimeConsumerGroupBoundToTrigger):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "realtime_group_bound_to_trigger", "Consumer group is bound to a trigger",
			"a realtime trigger consumes this group; delete the trigger to claim or settle its messages directly").WithDocs("https://gregale.dev/docs/realtime#function-triggers"))
	case errors.Is(err, state.ErrRealtimeConsumerGroupLease):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "realtime_group_lease_lost", "Claim lease lost", "the claim expired, was already settled, or has been reassigned; this token can no longer complete it"))
	case errors.Is(err, state.ErrRealtimeConsumerGroupGap):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "realtime_group_resync_required", "Consumer group requires recovery", "unassigned messages are outside retained history; inspect group bounds, recover from application state and explicitly resynchronize"))
	case errors.Is(err, state.ErrRealtimeConsumerGroupLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Consumer group capacity reached", realtimeConsumerGroupCapacityDetail(err)))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Consumer group conflict", "group already exists, recovery cursor changed, work is still pending, or the message is not dead-lettered"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "realtime consumer group not found")
	default:
		s.Host.Logger().WarnContext(r.Context(), "realtime consumer group operation failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("consumer group unavailable"))
	}
}
func (s *Service) RealtimeConsumerGroups(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	channel := r.PathValue("channel")
	if r.Method == http.MethodGet {
		groups, err := store.ListRealtimeConsumerGroups(r.Context(), ep.ID, channel)
		if err != nil {
			s.writeRealtimeConsumerGroupError(w, r, err)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, groups)
		return
	}
	var req struct {
		Name string `json:"name"`
		state.RealtimeConsumerGroupConfig
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupCreateBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid consumer group configuration"))
		return
	}
	group, err := store.CreateRealtimeConsumerGroup(r.Context(), ep.ID, channel, req.Name, req.RealtimeConsumerGroupConfig)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeConsumerGroup(r, acct, ep.ID, req.Name, "created")
	httpkit.WriteJSON(w, http.StatusCreated, group)
}
func (s *Service) RealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	name := r.PathValue("group")
	if r.Method == http.MethodDelete {
		if err := store.DeleteRealtimeConsumerGroup(r.Context(), ep.ID, r.PathValue("channel"), name); err != nil {
			s.writeRealtimeConsumerGroupError(w, r, err)
			return
		}
		s.auditRealtimeConsumerGroup(r, acct, ep.ID, name, "deleted")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	groups, err := store.ListRealtimeConsumerGroups(r.Context(), ep.ID, r.PathValue("channel"))
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	for _, g := range groups {
		if g.Name == name {
			httpkit.WriteJSON(w, http.StatusOK, g)
			return
		}
	}
	httpkit.NotFound(w, "realtime consumer group not found")
}
func (s *Service) ClaimRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	var req struct {
		Worker      string `json:"worker"`
		WaitSeconds int    `json:"wait_seconds,omitempty"`
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker identity"))
		return
	}
	claim, err := s.claimRealtimeGroupWithWait(r, store, ep.ID, r.PathValue("channel"), r.PathValue("group"), req.Worker, req.WaitSeconds)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	if claim == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, claim)
}
func (s *Service) SettleRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	var req state.RealtimeConsumerGroupSettlement
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid claim settlement"))
		return
	}
	result, err := store.SettleRealtimeConsumerGroup(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) RealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	letters, err := store.ListRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, letters)
}
func (s *Service) ReplayRealtimeConsumerGroupDeadLetter(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	if err := store.ReplayRealtimeConsumerGroupDeadLetter(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), r.PathValue("sequence")); err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeConsumerGroup(r, acct, ep.ID, r.PathValue("group"), "replayed")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) ResetRealtimeConsumerGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, store, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	var req struct {
		ExpectedSequence string `json:"expected_sequence"`
		AfterSequence    string `json:"after_sequence"`
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid group recovery cursor"))
		return
	}
	if err := store.ResetRealtimeConsumerGroup(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req.ExpectedSequence, req.AfterSequence); err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.auditRealtimeConsumerGroup(r, acct, ep.ID, r.PathValue("group"), "resynchronized")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) auditRealtimeConsumerGroup(r *http.Request, acct state.Account, ep, group, action string) {
	s.Host.EmitAudit(r.Context(), "realtime.consumer_group_"+action, &acct.ID, map[string]any{"endpoint_id": ep, "channel": r.PathValue("channel"), "group": group})
}

func realtimeConsumerGroupCapacityDetail(err error) string {
	var capacity *state.RealtimeConsumerGroupCapacityError
	if errors.As(err, &capacity) {
		if capacity.Resource == "settlement receipts" {
			return fmt.Sprintf("settlement receipts: limit %d, observed %d; wait for receipt expiry or replay an existing key; see https://gregale.dev/docs/realtime", capacity.Limit, capacity.Observed)
		}
		return fmt.Sprintf("%s: limit %d, observed %d; complete work, replay failures or delete an unused group; see https://gregale.dev/docs/realtime", capacity.Resource, capacity.Limit, capacity.Observed)
	}
	return "consumer group capacity reached; see https://gregale.dev/docs/realtime"
}
