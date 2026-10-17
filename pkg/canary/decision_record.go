package canary

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// The decision-recording APID surface (ADR-1447). Each is optional so the
// package's small fakes keep compiling; the production InternalSafeDeployClient
// implements all three.
type decisionAdvanceClient interface {
	AdvanceCanaryWithDecision(ctx context.Context, id string, expectedStep int, decision api.RolloutDecision) (api.CanaryAdvanceResponse, error)
}

type decisionRecoveryClient interface {
	RecoverDeploymentRolloutWithDecision(ctx context.Context, deploymentID, predecessorDeploymentID, action, reason, idempotencyKey string, decision api.RolloutDecision) (api.RolloutTransitionResponse, error)
}

type rolloutHoldRecorder interface {
	RecordRolloutHold(ctx context.Context, id string, expectedStep int, decision api.RolloutDecision) (api.RecordRolloutHoldResponse, error)
}

// circuitBreakerRule maps a breaker decision onto ADR-1447's closed rule set,
// which reuses the canary_progression_circuit_breaker_total event names.
func circuitBreakerRule(d CircuitBreakerDecision) string {
	switch d.Action {
	case CircuitBreakerAdvance:
		if d.Reason == "no stable predecessor" {
			return api.RolloutRuleAdvanceNoPredecessor
		}
		return api.RolloutRuleAdvanceHealthy
	case CircuitBreakerAbort:
		if rule := circuitBreakerAbortEvent(d.Reason); api.RolloutRuleKind(rule) == api.RolloutDecisionAbort {
			return rule
		}
		return ""
	default:
		return circuitBreakerHoldEvent(d.Reason)
	}
}

// rolloutDecisionRecord builds the aggregate-only evidence for one decision.
// observation is nil when the breaker could not read its signals.
func rolloutDecisionRecord(row CanaryRow, rule, summary string, observation *CircuitBreakerObservation, since, now time.Time) api.RolloutDecision {
	record := api.RolloutDecision{
		Kind: api.RolloutRuleKind(rule), Rule: rule,
		Summary: safetext.Truncate(summary, api.RolloutDecisionSummaryMaxBytes),
		Step:    row.CanaryStep, TrafficPercent: row.TrafficPercent,
		DecidedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if !since.IsZero() {
		record.WindowStart = since.UTC().Format(time.RFC3339Nano)
		record.WindowEnd = record.DecidedAt
	}
	if observation == nil || !observation.HasStable {
		return record
	}
	record.StableDeploymentID = observation.StableDeploymentID
	record.Candidate = decisionSample(observation.Candidate)
	record.Stable = decisionSample(observation.Stable)
	record.ProbesIncluded = strings.Contains(summary, "synthetic probes included") ||
		rule == api.RolloutRuleAdvanceProbeEvidence
	if observation.OOMSignalAvailable && observation.LivenessSignalAvailable {
		record.Signals = &api.RolloutDecisionSignals{
			OOMKills:                  nonNegative(observation.OOMKills),
			CandidateLivenessRestarts: nonNegative(observation.CandidateLivenessRestarts),
			StableLivenessRestarts:    nonNegative(observation.StableLivenessRestarts),
		}
	}
	return record
}

func decisionSample(w HealthWindow) *api.RolloutDecisionSample {
	return &api.RolloutDecisionSample{
		Requests: w.Requests, ServerErrors: w.ServerErrors,
		ProbeRequests: w.ProbeRequests, ProbeServerErrors: w.ProbeServerErrors,
		P95LatencyMS: nonNegative(w.P95LatencyMS), ColdBootRequests: w.ColdBootRequests,
		ColdBootP95LatencyMS: nonNegative(w.ColdBootP95LatencyMS),
		CPURequests:          w.CPURequests, CPUUsec: w.CPUUsec,
		DependencyCalls: w.DependencyCalls, DependencyErrors: w.DependencyErrors,
	}
}

// nonNegative drops NaN and negative readings (a counter reset between
// scrapes) so a record always passes APID's validation.
func nonNegative(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0
	}
	return f
}

// recordHold reports a hold to APID (ADR-1447). It is best effort: a hold
// changes no traffic, so a failed report is logged and retried next tick.
func (p *Progression) recordHold(ctx context.Context, row CanaryRow, record api.RolloutDecision) {
	recorder, ok := p.APID.(rolloutHoldRecorder)
	if !ok || record.Rule == "" {
		return
	}
	if err := record.Validate(); err != nil {
		p.Log.Warn("canary: rollout hold record invalid; not reported",
			"deployment_id", row.ID, "rule", record.Rule, "err", err)
		return
	}
	if _, err := recorder.RecordRolloutHold(ctx, row.ID, row.CanaryStep, record); err != nil {
		p.Log.Warn("canary: rollout hold report failed",
			"deployment_id", row.ID, "rule", record.Rule, "err", err)
	}
}
