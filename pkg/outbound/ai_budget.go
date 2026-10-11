package outbound

import "time"

// aiBudgetState is the settled AI usage the admission row holds for today
// and this calendar month (ADR-979, ADR-983).
type aiBudgetState struct {
	dailyTokens, bindingTokens, dailyCost, monthlyCost int64
}

// aiBudgetLimits are the effective AI limits; nil means unlimited.
type aiBudgetLimits struct {
	dailyTokens, bindingTokens, dailyCost, monthlyCost *int64
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

// aiBudgetExhausted reports the first exhausted AI budget and how long until
// it resets. Usage is known only after each response, so these stop new
// calls once settled usage reaches a limit.
func aiBudgetExhausted(utcNow time.Time, state aiBudgetState, limits aiBudgetLimits) (string, time.Duration, bool) {
	nextDay := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(utcNow.Year(), utcNow.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	checks := []struct {
		limit  *int64
		used   int64
		reason string
		resets time.Time
	}{
		{limits.dailyTokens, state.dailyTokens, ReasonDailyTokenLimit, nextDay},
		{limits.bindingTokens, state.bindingTokens, ReasonDailyTokenLimit, nextDay},
		{limits.dailyCost, state.dailyCost, ReasonDailyCostLimit, nextDay},
		{limits.monthlyCost, state.monthlyCost, ReasonMonthlyCostLimit, nextMonth},
	}
	for _, check := range checks {
		if check.limit != nil && check.used >= *check.limit {
			retry := check.resets.Sub(utcNow)
			if retry < time.Millisecond {
				retry = time.Millisecond
			}
			return check.reason, retry, true
		}
	}
	return "", 0, false
}
