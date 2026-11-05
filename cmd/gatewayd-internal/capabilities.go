package main

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
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

// The logs kill switches. apid hides the logs pages and the drain routes, but
// the customer logs route (live and archive) and drain delivery are served
// here, so gatewayd-internal decides them too: an off `logs` hides
// GET /v1/apps/{slug}/logs for the caller's account, an off `log-drains`
// stops delivering the drains of the app's account.
const (
	capabilityLogs      = "logs"
	capabilityLogDrains = "log-drains"
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

// logsReleased hides the logs route from a caller whose account has the logs
// capability off. It runs before the app is resolved and answers as apid
// answers a missing /v1 route, so a hidden route never reveals whether the
// app exists.
func logsReleased(allowed func(capability, accountID string) bool, next mwauth.AccountHandler) mwauth.AccountHandler {
	if allowed == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !allowed(capabilityLogs, acct.ID) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such API route"))
			return
		}
		next(w, r, acct)
	}
}
