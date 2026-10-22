package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"time"
)

func (s *Service) claimRealtimeGroupWithWait(r *http.Request, store state.RealtimeConsumerGroupStore, ep, ch, group, worker string, seconds int) (*state.RealtimeConsumerGroupClaim, error) {
	if seconds == 0 {
		return store.ClaimRealtimeConsumerGroup(r.Context(), ep, ch, group, worker)
	}
	batch, err := s.waitRealtimeGroupBatch(r, ep, ch, group, worker, 1, seconds)
	if err != nil {
		return nil, err
	}
	if len(batch.Claims) != 0 {
		return &batch.Claims[0], nil
	}
	if batch.DeduplicationBlocked {
		return nil, &state.RealtimeConsumerGroupDeduplicationFullError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.CircuitOpen {
		return nil, &state.RealtimeConsumerGroupCircuitOpenError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.RateThrottled {
		return nil, &state.RealtimeConsumerGroupRateLimitError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.HistoryUnavailable {
		return nil, state.ErrRealtimeConsumerGroupGap
	}
	if batch.CapacityBlocked {
		return nil, &state.RealtimeConsumerGroupCapacityError{Resource: "outstanding messages per group", Limit: api.RealtimeConsumerGroupOutstandingMax, Observed: api.RealtimeConsumerGroupOutstandingMax}
	}
	return nil, nil
}
func (s *Service) waitRealtimeGroupBatch(r *http.Request, ep, ch, group, worker string, limit, seconds int) (state.RealtimeConsumerGroupBatch, error) {
	var result state.RealtimeConsumerGroupBatch
	if seconds < 0 || seconds > api.RealtimeConsumerGroupWaitMaxSeconds {
		return result, state.ErrRealtimeConsumerGroupInvalid
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupBatchStore)
	if !ok {
		return result, state.ErrRealtimeConsumerGroupLimit
	}
	if seconds == 0 {
		return store.ClaimRealtimeConsumerGroupBatch(r.Context(), ep, ch, group, worker, limit)
	}
	s.ClaimWaitOnce.Do(func() { s.ClaimWaitSlots = make(chan struct{}, api.RealtimeConsumerGroupWaitersMax) })
	select {
	case s.ClaimWaitSlots <- struct{}{}:
		defer func() { <-s.ClaimWaitSlots }()
	default:
		return result, &state.RealtimeConsumerGroupCapacityError{Resource: "long-poll requests per apid", Limit: api.RealtimeConsumerGroupWaitersMax, Observed: api.RealtimeConsumerGroupWaitersMax}
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		var err error
		result, err = store.ClaimRealtimeConsumerGroupBatch(r.Context(), ep, ch, group, worker, limit)
		if err != nil || len(result.Claims) != 0 || result.Paused || result.DeduplicationBlocked || result.CircuitOpen || result.RateThrottled || result.FilterScanLimited || result.HistoryUnavailable || result.CapacityBlocked {
			return result, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return result, nil
		}
		timer := time.NewTimer(min(remaining, time.Duration(api.RealtimeConsumerGroupWaitPollMillis)*time.Millisecond))
		select {
		case <-r.Context().Done():
			timer.Stop()
			return result, r.Context().Err()
		case <-timer.C:
		}
		if time.Until(deadline) <= 0 {
			return result, nil
		}
	}
}

// Match escaped path segments so encoded channel/group separators stay within
// their route parameter and unrelated admin routes retain the default budget.
func IsRealtimeGroupClaimRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
	if len(parts) == 6 || len(parts) == 7 {
		return parts[0] == "v1" && parts[1] == "realtime" && parts[2] == "workers" && parts[3] == "groups" && parts[5] == "claims" && (len(parts) == 6 || parts[6] == "batch")
	}
	if len(parts) == 11 || len(parts) == 12 {
		return parts[0] == "v1" && parts[1] == "apps" && parts[3] == "realtime" && parts[4] == "endpoints" && parts[6] == "channels" && parts[8] == "groups" && parts[10] == "claims" && (len(parts) == 11 || parts[11] == "batch")
	}
	return false
}
