package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// Capability flags (ADR-20261023115741) gatewayd-internal decides per
// request for the account that owns a rule. An off capability drops the rules
// that add behavior (response caching, async routes, retries, breaker
// tuning); restrictive kinds keep enforcing.
const (
	capabilityResponseCaching = "declarative-response-caching"
	capabilityAsyncRoutes     = "async-routes"
	capabilityRequestRetry    = "request-retry"
	capabilityCircuitBreaking = "circuit-breaking"
)

// capabilityDecision decides capability flags from the daemon's platform-flag
// set, which the runtime-config watcher keeps current. gatewayd-internal
// knows only the account, so flags it consumes take account rules alone.
func capabilityDecision(flags *platformflags.Set) (func(capability, accountID string) bool, error) {
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("gatewayd: load capability catalog: %w", err)
	}
	return func(capability, accountID string) bool {
		return decider.Enabled(capability, platformflags.Subject{AccountID: accountID})
	}, nil
}

// capabilityRules drops the rules whose account has capability off. It
// copies only when it drops a rule, so the cached slice is never modified.
func capabilityRules[T any](allowed func(capability, accountID string) bool, capability string, rules []T, account func(*T) string) []T {
	if allowed == nil {
		return rules
	}
	var kept []T
	for i := range rules {
		on := allowed(capability, account(&rules[i]))
		switch {
		case on && kept != nil:
			kept = append(kept, rules[i])
		case !on && kept == nil:
			kept = append(make([]T, 0, len(rules)), rules[:i]...)
		}
	}
	if kept == nil {
		return rules
	}
	return kept
}
