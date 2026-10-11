package outbound

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// validateAIAdmissionSpec bounds the ADR-984 admission inputs shared by
// every backend.
func validateAIAdmissionSpec(spec AdmissionSpec) error {
	if spec.AIReservedTokens < 0 || spec.AIReservedTokens > 3*api.OutboundAIMaxTokensPerResponse ||
		spec.AIReservedCostMillicents < 0 || spec.AIReservedCostMillicents > api.MaxOutboundAICostMillicents {
		return fmt.Errorf("%w: AI reservation is outside the supported range", ErrInvalidIntegration)
	}
	if spec.BindingRequestsPerMinute != nil && (spec.BindingAppID == "" || *spec.BindingRequestsPerMinute < 1 ||
		*spec.BindingRequestsPerMinute > api.MaxOutboundAIRequestsPerMinute) {
		return fmt.Errorf("%w: binding requests-per-minute limit is invalid", ErrInvalidIntegration)
	}
	if spec.BindingTokensPerMinute != nil && (spec.BindingAppID == "" || *spec.BindingTokensPerMinute < 1 ||
		*spec.BindingTokensPerMinute > api.MaxOutboundAITokensPerMinute) {
		return fmt.Errorf("%w: binding tokens-per-minute limit is invalid", ErrInvalidIntegration)
	}
	return nil
}

// aiBudgetState is the settled AI usage the admission row holds for today
// and this calendar month (ADR-979, ADR-983), plus the reservations of live
// leases (ADR-984).
type aiBudgetState struct {
	dailyTokens, bindingTokens, dailyCost, monthlyCost  int64
	inflightTokens, inflightBindingTokens, inflightCost int64
}

// aiBudgetLimits are the effective AI limits; nil means unlimited.
type aiBudgetLimits struct {
	dailyTokens, bindingTokens, dailyCost, monthlyCost *int64
}

// aiReservation is one call's admission-time estimate (ADR-984).
type aiReservation struct {
	tokens, cost int64
}

// positiveLimit maps a stored COALESCE(limit, 0) value to an optional limit.
func positiveLimit(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func aiBudgetLimitsFromSpec(spec AdmissionSpec) aiBudgetLimits {
	return aiBudgetLimits{
		dailyTokens: spec.DailyAITokenLimit, bindingTokens: spec.BindingDailyAITokenLimit,
		dailyCost: spec.DailyAICostLimit, monthlyCost: spec.MonthlyAICostLimit,
	}
}

func aiReservationFromSpec(spec AdmissionSpec) aiReservation {
	return aiReservation{tokens: max(spec.AIReservedTokens, 0), cost: max(spec.AIReservedCostMillicents, 0)}
}

// aiBudgetExhausted reports the first AI budget the call cannot fit and how
// long until it resets. A budget rejects once settled usage reaches it, and
// also when settled usage plus live reservations plus this call's
// reservation would pass it (ADR-984), so a limit is a hard cap for the
// gateway's estimate.
func aiBudgetExhausted(utcNow time.Time, state aiBudgetState, limits aiBudgetLimits, reserve aiReservation) (string, time.Duration, bool) {
	nextDay := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(utcNow.Year(), utcNow.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	checks := []struct {
		limit            *int64
		used, held, call int64
		reason           string
		resets           time.Time
	}{
		{limits.dailyTokens, state.dailyTokens, state.inflightTokens, reserve.tokens, ReasonDailyTokenLimit, nextDay},
		{limits.bindingTokens, state.bindingTokens, state.inflightBindingTokens, reserve.tokens, ReasonDailyTokenLimit, nextDay},
		{limits.dailyCost, state.dailyCost, state.inflightCost, reserve.cost, ReasonDailyCostLimit, nextDay},
		{limits.monthlyCost, state.monthlyCost, state.inflightCost, reserve.cost, ReasonMonthlyCostLimit, nextMonth},
	}
	for _, check := range checks {
		if check.limit == nil {
			continue
		}
		if check.used >= *check.limit || check.used+check.held+check.call > *check.limit {
			return check.reason, retryUntil(utcNow, check.resets), true
		}
	}
	return "", 0, false
}

// aiMinuteState is an app's usage in the current UTC minute.
type aiMinuteState struct {
	requests, tokens, inflightTokens int64
}

// aiMinuteExhausted applies an app's per-minute limits (ADR-984). Requests
// count admitted calls; tokens count settled tokens in the window, live
// reservations and this call's reservation.
func aiMinuteExhausted(utcNow time.Time, state aiMinuteState, requestsPerMinute, tokensPerMinute *int64, reserve aiReservation) (string, time.Duration, bool) {
	nextMinute := utcNow.Truncate(time.Minute).Add(time.Minute)
	if requestsPerMinute != nil && state.requests >= *requestsPerMinute {
		return ReasonRequestsPerMinute, retryUntil(utcNow, nextMinute), true
	}
	if tokensPerMinute != nil && (state.tokens >= *tokensPerMinute || state.tokens+state.inflightTokens+reserve.tokens > *tokensPerMinute) {
		return ReasonTokensPerMinute, retryUntil(utcNow, nextMinute), true
	}
	return "", 0, false
}

func retryUntil(now, resets time.Time) time.Duration {
	retry := resets.Sub(now)
	if retry < time.Millisecond {
		retry = time.Millisecond
	}
	return retry
}
