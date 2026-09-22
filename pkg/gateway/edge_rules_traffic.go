package gateway

// Resolved subsets for the ADR-201 traffic-resilience kinds (retry,
// circuit_breaker).
//
// Same shape as every kind since PR 3: a narrow struct carrying only the
// fields the hot path reads, a PickFirstXMatch filter over the
// priority-ordered slice, and a HostEntry slot the cmd-side loader fills.
// pkg/gateway does not import pkg/state, so the state.EdgeRule → *Resolved
// conversion happens in cmd/gatewayd-internal/edge_rules.go.

import "time"

// EdgeRuleRetryResolved is the kind=retry subset the matcher reads.
//
// The compiled form is already clamped by the cmd-side compiler, so the hot
// path performs no range checks: a rule that reached here is safe to apply
// verbatim. There is no "retry on status" field because only a transport
// failure may arm a replay (ADR-201 §1).
type EdgeRuleRetryResolved struct {
	ID                 string
	AccountID          string
	AppID              string
	Priority           int
	PathGlob           string          // "" = any path
	Methods            map[string]bool // nil = any method
	MaxAttempts        int             // always >= 2 post-compile
	AllowNonIdempotent bool
	MinRemaining       time.Duration
	Backoff            time.Duration
	BudgetPercent      int
	BudgetMinRetries   int
}

// Policy converts the resolved rule into the runtime policy the retry loop
// consumes. Kept here rather than in retry.go so the wire subset and the
// runtime shape are adjacent and cannot drift apart silently.
func (r *EdgeRuleRetryResolved) Policy() RetryPolicy {
	if r == nil {
		return RetryPolicy{}
	}
	return RetryPolicy{
		Enabled:            true,
		MaxAttempts:        r.MaxAttempts,
		AllowNonIdempotent: r.AllowNonIdempotent,
		MinRemaining:       r.MinRemaining,
		Backoff:            r.Backoff,
		BudgetPercent:      r.BudgetPercent,
		BudgetMinRetries:   r.BudgetMinRetries,
	}
}

// EdgeRuleCircuitBreakerResolved is the kind=circuit_breaker subset.
//
// Every field is already defaulted and clamped post-compile, so a zero here
// means the customer's stored value, not "unset".
type EdgeRuleCircuitBreakerResolved struct {
	ID               string
	AccountID        string
	AppID            string
	Priority         int
	PathGlob         string
	Methods          map[string]bool
	FailureThreshold float64
	MinRequests      int
	Window           time.Duration
	OpenDuration     time.Duration
	MaxOpenDuration  time.Duration
}

// PickFirstRetryMatch is the priority-ASC + methods + path-glob filter over
// the compiled kind=retry slice. Mirrors PickFirstBudgetMatch exactly; the
// slice arrives priority-ordered from the cache.
func PickFirstRetryMatch(rules []EdgeRuleRetryResolved, requestPath, method string) *EdgeRuleRetryResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// PickFirstCircuitBreakerMatch is the kind=circuit_breaker equivalent.
func PickFirstCircuitBreakerMatch(rules []EdgeRuleCircuitBreakerResolved, requestPath, method string) *EdgeRuleCircuitBreakerResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}
