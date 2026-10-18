package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Observation and dispatch are separate transactions. Delivery failures never
// prevent later failure/recovery transitions from reaching durable history.
func (e *Evaluator) evalMCPReadinessIncident(ctx context.Context, rule state.AlertRule, statuses []api.MCPReadinessMonitorStatus, observed float64, breach bool, now time.Time, stats *Stats) {
	source, ok := e.store.(state.MCPReadinessOutboxStore)
	if !ok {
		stats.SkippedDegraded++
		return
	}
	paths := e.investigationPaths(ctx, rule)
	paths.mcpReadiness = statuses
	payload, _, err := buildPayload(rule, observed, paths, nil)
	if err != nil {
		return
	}
	transition, err := source.RecordMCPReadinessObservation(ctx, state.MCPReadinessObservation{Rule: rule, At: now, Breached: breach, Coverage: readinessCoverageFingerprint(statuses), Payload: payload, ObservedValue: observed})
	if err != nil {
		if !errors.Is(err, state.ErrConflict) {
			stats.SkippedDegraded++
			e.log.Warn("alerts: record readiness observation", "rule", rule.ID, "err", err)
		}
	} else if transition.EventID != "" {
		if transition.Event == "alert.fired" {
			stats.Fired++
			if e.ops != nil {
				e.ops.AlertEvalFiredTotal()()
			}
		}
		if e.audit != nil {
			e.audit.Emit(ctx, transition.Event, &rule.AccountID, map[string]any{"rule_id": rule.ID, "incident_id": transition.IncidentID, "event_id": transition.EventID})
		}
	}
}

func readinessCoverageFingerprint(statuses []api.MCPReadinessMonitorStatus) string {
	keys := make([]string, 0, len(statuses))
	seen := map[string]bool{}
	for _, s := range statuses {
		b, _ := json.Marshal([]string{api.MCPConnectionScope(s.Scope), s.Role})
		if !seen[string(b)] {
			keys = append(keys, string(b))
			seen[string(b)] = true
		}
	}
	sort.Strings(keys)
	b, _ := json.Marshal(keys)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func readinessEventScopes(statuses []api.MCPReadinessMonitorStatus) []string {
	seen := map[string]bool{}
	for _, s := range statuses {
		seen[api.MCPConnectionScope(s.Scope)] = true
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

// The oldest eligible receiver delivery is leased independently. Each dispatch is bounded
// below the lease duration, and completion is fenced by the fresh claim token.
func (e *Evaluator) retryMCPReadinessIncident(ctx context.Context, rule state.AlertRule, _ time.Time, stats *Stats) {
	source, ok := e.store.(state.MCPReadinessOutboxStore)
	if !ok {
		return
	}
	claim, won, err := source.ClaimMCPReadinessDelivery(ctx, rule.ID, e.now())
	if err != nil {
		e.log.Warn("alerts: claim readiness delivery", "rule", rule.ID, "err", err)
		return
	}
	if !won {
		return
	}
	if claim.Provider == "pagerduty" {
		e.deliverMCPReadinessPagerDuty(ctx, claim, stats)
		return
	}
	var payload map[string]any
	if json.Unmarshal(claim.Delivery.Payload, &payload) == nil {
		dispatchCtx, cancel := context.WithTimeout(ctx, state.MCPReadinessDeliveryLease/2)
		e.deliverAlert(dispatchCtx, claim.Rule, claim.Delivery.ID, claim.Delivery.ObservedValue, payload, e.now(), stats)
		cancel()
	}
	// Use the parent context for acknowledgement after the transport deadline.
	if err := source.CompleteMCPReadinessDelivery(ctx, claim.EventID, claim.Token, e.now()); err != nil {
		e.log.Warn("alerts: complete readiness delivery", "rule", rule.ID, "err", err)
	}
}
