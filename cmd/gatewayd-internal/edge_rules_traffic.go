package main

// Compile + match for the ADR-201 traffic-resilience kinds (retry,
// circuit_breaker).
//
// Mirrors compileBudgetRules / MatchBudget exactly. apid-Validate already
// applied the defaults and bounds; this pass is the defence-in-depth layer
// for a row that reached Postgres another way — a direct DB write, or the e2e
// helper seedEdgeRuleDirect. A rule that cannot be made safe here is dropped
// from the compiled slice rather than clamped into something the customer did
// not ask for: the customer then sees the ordinary pass-through behaviour,
// which is the same posture compileLimitRules takes on a malformed cap.

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func compileRetryRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleRetryResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleRetryResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled || r.Kind != state.EdgeRuleKindRetry || r.Action.Retry == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		// A rule that cannot replay is DROPPED, not clamped up to the
		// default. Clamping would silently enable retry on a route whose
		// stored row says otherwise, which is the wrong direction for a
		// primitive that can double a side effect.
		attempts := r.Action.Retry.MaxAttempts
		if attempts < 2 {
			continue
		}
		if attempts > api.EdgeRuleRetryMaxAttempts {
			attempts = api.EdgeRuleRetryMaxAttempts
		}
		minRemaining := r.Action.Retry.MinRemainingMs
		if minRemaining < 0 || minRemaining > api.MaxEdgeRuleRetryMinRemainingMs {
			minRemaining = api.EdgeRuleRetryDefaultMinRemainingMs
		}
		backoff := r.Action.Retry.BackoffMs
		if backoff < 0 || backoff > api.MaxEdgeRuleRetryBackoffMs {
			backoff = 0
		}
		budgetPercent := r.Action.Retry.BudgetPercent
		if budgetPercent < 1 || budgetPercent > api.MaxEdgeRuleRetryBudgetPercent {
			budgetPercent = api.EdgeRuleRetryDefaultBudgetPercent
		}
		budgetMinRetries := r.Action.Retry.BudgetMinRetries
		if budgetMinRetries < 0 || budgetMinRetries > api.MaxEdgeRuleRetryBudgetMin {
			budgetMinRetries = api.EdgeRuleRetryDefaultBudgetMin
		}
		out = append(out, gateway.EdgeRuleRetryResolved{
			ID:                 r.ID,
			AccountID:          r.AccountID,
			AppID:              r.AppID,
			Priority:           r.Priority,
			PathGlob:           r.MatchPath,
			Methods:            buildMethodsMap(r.MatchMethods),
			MaxAttempts:        attempts,
			AllowNonIdempotent: r.Action.Retry.AllowNonIdempotent,
			MinRemaining:       time.Duration(minRemaining) * time.Millisecond,
			Backoff:            time.Duration(backoff) * time.Millisecond,
			BudgetPercent:      budgetPercent,
			BudgetMinRetries:   budgetMinRetries,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

func compileCircuitBreakerRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleCircuitBreakerResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleCircuitBreakerResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled || r.Kind != state.EdgeRuleKindCircuitBreaker || r.Action.CircuitBreaker == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		cb := r.Action.CircuitBreaker
		threshold := cb.FailureThreshold
		if threshold <= 0 || threshold > 1 {
			threshold = api.EdgeRuleCircuitDefaultFailureThreshold
		}
		minRequests := cb.MinRequests
		if minRequests < 1 || minRequests > api.MaxEdgeRuleCircuitMinRequests {
			minRequests = api.EdgeRuleCircuitDefaultMinRequests
		}
		window := cb.WindowSeconds
		if window < 1 || window > api.MaxEdgeRuleCircuitWindowSeconds {
			window = api.EdgeRuleCircuitDefaultWindowSeconds
		}
		open := cb.OpenSeconds
		if open < 1 || open > api.MaxEdgeRuleCircuitOpenSeconds {
			open = api.EdgeRuleCircuitDefaultOpenSeconds
		}
		maxOpen := cb.MaxOpenSeconds
		if maxOpen < open || maxOpen > api.MaxEdgeRuleCircuitOpenSeconds {
			// The backoff ceiling can never be below the interval it
			// grows from; lifting it to `open` keeps the breaker
			// well-formed (no growth) rather than dropping protection
			// the customer asked for.
			maxOpen = open
		}
		out = append(out, gateway.EdgeRuleCircuitBreakerResolved{
			ID:               r.ID,
			AccountID:        r.AccountID,
			AppID:            r.AppID,
			Priority:         r.Priority,
			PathGlob:         r.MatchPath,
			Methods:          buildMethodsMap(r.MatchMethods),
			FailureThreshold: threshold,
			MinRequests:      minRequests,
			Window:           time.Duration(window) * time.Second,
			OpenDuration:     time.Duration(open) * time.Second,
			MaxOpenDuration:  time.Duration(maxOpen) * time.Second,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// MatchRetry is the ADR-201 §1 matcher. Same shape as MatchBudget: a cache
// hit returns immediately; a miss triggers loadHost, which compiles every
// kind's slice in one SQL roundtrip.
func (g *gatewaydEdgeRules) MatchRetry(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleRetryResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetRetry(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Retry
	}
	return gateway.PickFirstRetryMatch(rules, requestPath, method)
}

// MatchCircuitBreaker is the ADR-201 §2 matcher.
func (g *gatewaydEdgeRules) MatchCircuitBreaker(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleCircuitBreakerResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetCircuitBreaker(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.CircuitBreaker
	}
	return gateway.PickFirstCircuitBreakerMatch(rules, requestPath, method)
}
