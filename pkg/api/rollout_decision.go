package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Rollout decision kinds (ADR-1447).
const (
	RolloutDecisionAdvance = "advance"
	RolloutDecisionHold    = "hold"
	RolloutDecisionAbort   = "abort"
)

// Rollout decision rules (ADR-1447). The breaker rules reuse the
// canary_progression_circuit_breaker_total event names, so a receipt, a
// metric and the runbook use one vocabulary.
const (
	RolloutRuleAdvanceHealthy        = "advance_healthy"
	RolloutRuleAdvanceProbeEvidence  = "advance_probe_evidence"
	RolloutRuleAdvanceLowTraffic     = "advance_low_traffic"
	RolloutRuleAdvanceNoPredecessor  = "advance_no_predecessor"
	RolloutRuleAdvanceManual         = "advance_manual"
	RolloutRuleHoldInsufficient      = "hold_insufficient_samples"
	RolloutRuleHoldSignalUnavailable = "hold_signal_unavailable"
	RolloutRuleHoldObservation       = "hold_observation_unavailable"
	RolloutRuleHoldHealthAlert       = "hold_health_alert"
	RolloutRuleAbort5xx              = "abort_5xx"
	RolloutRuleAbortP95Latency       = "abort_p95_latency"
	RolloutRuleAbortColdBootP95      = "abort_cold_boot_p95"
	RolloutRuleAbortCPUPerRequest    = "abort_cpu_per_request"
	RolloutRuleAbortDependencyErrors = "abort_dependency_errors"
	RolloutRuleAbortOOM              = "abort_oom"
	RolloutRuleAbortCrashLoop        = "abort_crash_loop"
	RolloutRuleAbortManual           = "abort_manual"
	RolloutRuleAbortLeaseExpired     = "abort_worker_lease_expired"
	// Post-release watch (ADR-1445): regressions after the rollout completed.
	RolloutRuleAbortPostRelease5xx = "abort_post_release_5xx"
	RolloutRuleAbortPostReleaseP95 = "abort_post_release_p95_latency"
)

var rolloutDecisionRules = map[string]string{
	RolloutRuleAdvanceHealthy:        RolloutDecisionAdvance,
	RolloutRuleAdvanceProbeEvidence:  RolloutDecisionAdvance,
	RolloutRuleAdvanceLowTraffic:     RolloutDecisionAdvance,
	RolloutRuleAdvanceNoPredecessor:  RolloutDecisionAdvance,
	RolloutRuleAdvanceManual:         RolloutDecisionAdvance,
	RolloutRuleHoldInsufficient:      RolloutDecisionHold,
	RolloutRuleHoldSignalUnavailable: RolloutDecisionHold,
	RolloutRuleHoldObservation:       RolloutDecisionHold,
	RolloutRuleHoldHealthAlert:       RolloutDecisionHold,
	RolloutRuleAbort5xx:              RolloutDecisionAbort,
	RolloutRuleAbortP95Latency:       RolloutDecisionAbort,
	RolloutRuleAbortColdBootP95:      RolloutDecisionAbort,
	RolloutRuleAbortCPUPerRequest:    RolloutDecisionAbort,
	RolloutRuleAbortDependencyErrors: RolloutDecisionAbort,
	RolloutRuleAbortOOM:              RolloutDecisionAbort,
	RolloutRuleAbortCrashLoop:        RolloutDecisionAbort,
	RolloutRuleAbortManual:           RolloutDecisionAbort,
	RolloutRuleAbortLeaseExpired:     RolloutDecisionAbort,
	RolloutRuleAbortPostRelease5xx:   RolloutDecisionAbort,
	RolloutRuleAbortPostReleaseP95:   RolloutDecisionAbort,
}

// RolloutDecision is the evidence behind one decision about a rollout
// (ADR-1447): why a canary stage advanced, why it is held, or why it was
// aborted. It carries only metric names and aggregates; no request payload,
// route, URL, trace ID or customer text.
type RolloutDecision struct {
	Kind    string `json:"kind"`
	Rule    string `json:"rule"`
	Summary string `json:"summary,omitempty"`
	// Step is the zero-based canary step that was evaluated, and
	// TrafficPercent the candidate's share during the observation window.
	Step               int                    `json:"step"`
	TrafficPercent     int                    `json:"traffic_percent"`
	WindowStart        string                 `json:"window_start,omitempty"`
	WindowEnd          string                 `json:"window_end,omitempty"`
	StableDeploymentID string                 `json:"stable_deployment_id,omitempty"`
	Candidate          *RolloutDecisionSample `json:"candidate,omitempty"`
	Stable             *RolloutDecisionSample `json:"stable,omitempty"`
	// ProbesIncluded marks a decision whose 5xx sample included synthetic
	// probes of the app (ADR-1446).
	ProbesIncluded bool `json:"probes_included,omitempty"`
	// Signals is present when the OOM and liveness signals were readable.
	Signals   *RolloutDecisionSignals `json:"signals,omitempty"`
	DecidedAt string                  `json:"decided_at"`
}

// RolloutDecisionSample is one side's aggregates over the window. Request
// counts are organic requests; probe counts are reported separately.
type RolloutDecisionSample struct {
	Requests             int64   `json:"requests"`
	ServerErrors         int64   `json:"server_errors"`
	ProbeRequests        int64   `json:"probe_requests,omitempty"`
	ProbeServerErrors    int64   `json:"probe_server_errors,omitempty"`
	P95LatencyMS         float64 `json:"p95_latency_ms,omitempty"`
	ColdBootRequests     int64   `json:"cold_boot_requests,omitempty"`
	ColdBootP95LatencyMS float64 `json:"cold_boot_p95_latency_ms,omitempty"`
	CPURequests          int64   `json:"cpu_requests,omitempty"`
	CPUUsec              int64   `json:"cpu_usec,omitempty"`
	DependencyCalls      int64   `json:"dependency_calls,omitempty"`
	DependencyErrors     int64   `json:"dependency_errors,omitempty"`
}

// RolloutDecisionSignals carries the candidate's OOM kills and both sides'
// liveness-driven restarts over the window.
type RolloutDecisionSignals struct {
	OOMKills                  float64 `json:"oom_kills"`
	CandidateLivenessRestarts float64 `json:"candidate_liveness_restarts"`
	StableLivenessRestarts    float64 `json:"stable_liveness_restarts"`
}

// RecordRolloutHoldRequest is the body of meterd's loopback hold route,
// POST /v1/internal/safe-deploy/deployments/{id}/canary/hold. ExpectedStep
// keeps a delayed tick from recording a hold for a step that already moved.
type RecordRolloutHoldRequest struct {
	ExpectedStep int             `json:"expected_step"`
	Decision     RolloutDecision `json:"decision"`
}

// RecordRolloutHoldResponse reports whether the hold produced a new audit
// row; a repeated hold within RolloutHoldRefreshInterval does not.
type RecordRolloutHoldResponse struct {
	Recorded bool   `json:"recorded"`
	AuditID  string `json:"audit_id,omitempty"`
}

// RolloutRuleKind returns the kind a rule belongs to, or "" for an unknown
// rule.
func RolloutRuleKind(rule string) string {
	return rolloutDecisionRules[rule]
}

// Validate rejects a decision that is not closed-set, aggregate-only and
// within RolloutDecisionMaxBytes. APID validates every worker-supplied
// decision before it reaches the deployment row or the audit timeline.
func (d RolloutDecision) Validate() error {
	kind, ok := rolloutDecisionRules[d.Rule]
	if !ok {
		return fmt.Errorf("unknown rollout decision rule %q", d.Rule)
	}
	if d.Kind != kind {
		return fmt.Errorf("rollout decision rule %q is not a %q decision", d.Rule, d.Kind)
	}
	if len(d.Summary) > RolloutDecisionSummaryMaxBytes {
		return fmt.Errorf("rollout decision summary exceeds %d bytes", RolloutDecisionSummaryMaxBytes)
	}
	if !utf8.ValidString(d.Summary) || strings.IndexFunc(d.Summary, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("rollout decision summary must be printable UTF-8")
	}
	if d.Step < 0 || d.TrafficPercent < 0 || d.TrafficPercent > 100 {
		return errors.New("rollout decision step or traffic percent is out of range")
	}
	if _, err := time.Parse(time.RFC3339Nano, d.DecidedAt); err != nil {
		return errors.New("rollout decision decided_at must be an RFC 3339 timestamp")
	}
	for _, ts := range []string{d.WindowStart, d.WindowEnd} {
		if ts == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			return errors.New("rollout decision window bounds must be RFC 3339 timestamps")
		}
	}
	if d.StableDeploymentID != "" {
		if _, err := uuid.Parse(d.StableDeploymentID); err != nil {
			return errors.New("rollout decision stable_deployment_id must be a UUID")
		}
	}
	for _, sample := range []*RolloutDecisionSample{d.Candidate, d.Stable} {
		if sample != nil && !sample.valid() {
			return errors.New("rollout decision samples must be finite and non-negative")
		}
	}
	if s := d.Signals; s != nil && (!nonNegativeFinite(s.OOMKills) || !nonNegativeFinite(s.CandidateLivenessRestarts) || !nonNegativeFinite(s.StableLivenessRestarts)) {
		return errors.New("rollout decision signals must be finite and non-negative")
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode rollout decision: %w", err)
	}
	if len(encoded) > RolloutDecisionMaxBytes {
		return fmt.Errorf("rollout decision exceeds %d bytes", RolloutDecisionMaxBytes)
	}
	return nil
}

func (s RolloutDecisionSample) valid() bool {
	for _, n := range []int64{s.Requests, s.ServerErrors, s.ProbeRequests, s.ProbeServerErrors, s.ColdBootRequests, s.CPURequests, s.CPUUsec, s.DependencyCalls, s.DependencyErrors} {
		if n < 0 {
			return false
		}
	}
	return nonNegativeFinite(s.P95LatencyMS) && nonNegativeFinite(s.ColdBootP95LatencyMS)
}

func nonNegativeFinite(f float64) bool {
	return f >= 0 && !math.IsInf(f, 0) && !math.IsNaN(f)
}

// ManualRolloutDecision is the record APID writes for a transition it makes
// on a person's or the platform's behalf rather than on breaker evidence.
func ManualRolloutDecision(rule, summary string, step, trafficPercent int, at time.Time) RolloutDecision {
	return RolloutDecision{
		Kind: RolloutRuleKind(rule), Rule: rule, Summary: summary,
		Step: step, TrafficPercent: trafficPercent,
		DecidedAt: at.UTC().Format(time.RFC3339Nano),
	}
}
