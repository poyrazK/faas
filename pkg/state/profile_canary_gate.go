package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProfileCanaryGateReader interface {
	ReadProfileCanaryGate(context.Context, string, string, string) (api.ProfileCanaryGateDecision, error)
}

type ProfileGateBlockedError struct{ Decision api.ProfileCanaryGateDecision }

func (e *ProfileGateBlockedError) Error() string { return "canary profile gate: " + e.Decision.Reason }

func validateProfileCanaryGatePolicy(c api.ProfileDeploymentPolicyConfig) error {
	g := c.CanaryGate
	if g == nil {
		return nil
	}
	minimum := c.WarmupSeconds + c.WindowSeconds*g.Confirmations + int(api.ProfileAutoIngestionGrace/time.Second)
	if !c.Enabled || len(c.Options.Routes) == 0 || g.Confirmations < 1 || g.Confirmations > api.ProfileGateMaxConfirmations || g.TimeoutSeconds < minimum || g.TimeoutSeconds > api.ProfileGateMaxTimeoutSeconds || (g.OnTimeout != "hold" && g.OnTimeout != "continue") {
		return errors.New("canary gate requires enabled checks, explicit routes, 1–5 confirmations, hold or continue on timeout, and a timeout covering warmup plus confirmation windows and ingestion (maximum 86400 seconds)")
	}
	return nil
}

func profileGateAnchor(d Deployment, p api.ProfileDeploymentPolicy) time.Time {
	anchor := d.CanaryStepStartedAt.UTC()
	if p.UpdatedAt != nil && p.UpdatedAt.After(anchor) {
		anchor = p.UpdatedAt.UTC()
	}
	return anchor
}

func newProfileGateState(d Deployment, p api.ProfileDeploymentPolicy) *api.ProfileCanaryGateState {
	if p.Config.CanaryGate == nil {
		return nil
	}
	g := &api.ProfileCanaryGateState{Policy: *p.Config.CanaryGate, Status: "collecting", Reason: "Waiting for consecutive qualified route windows.", Deadline: profileGateAnchor(d, p).Add(time.Duration(p.Config.CanaryGate.TimeoutSeconds) * time.Second), Streaks: []api.ProfileGateRouteStreak{}}
	for _, route := range p.Config.Options.Routes {
		g.Streaks = append(g.Streaks, api.ProfileGateRouteStreak{Route: route, Status: "insufficient_data"})
	}
	return g
}

// The durable receipt retains the last checked windows. Work uses the separate
// next window so historical code evidence never acquires different query times.
func profileGateWork(in api.CanaryProfileSignal) api.CanaryProfileSignal {
	if in.Gate == nil || in.Gate.NextCandidate == nil {
		return in
	}
	c := *in.Gate.NextCandidate
	in.Candidate = &c
	if in.Baseline != nil {
		b := *in.Baseline
		b.Start, b.End = c.Start, c.End
		in.Baseline = &b
	}
	return in
}

func validateProfileGateAssessment(signal api.CanaryProfileSignal, a api.ProfileRegressionAssessment) error {
	if signal.Gate == nil {
		return nil
	}
	expected := profileGateWork(signal)
	if expected.Baseline == nil || expected.Candidate == nil || !sameInvestigationSelection(*expected.Baseline, a.Baseline) || !sameInvestigationSelection(*expected.Candidate, a.Candidate) {
		return errors.New("profile gate result does not match its claimed windows")
	}
	left, _ := json.Marshal(signal.Options)
	right, _ := json.Marshal(a.Options)
	if string(left) != string(right) || a.CheckedAt.IsZero() {
		return errors.New("profile gate result does not match its policy")
	}
	return nil
}

// Only distinct, nonoverlapping windows count. Unknown route evidence breaks
// its streak, and a threshold observation is never inferred from aggregate CPU.
func finishProfileGateWindow(signal *api.CanaryProfileSignal, a api.ProfileRegressionAssessment, retry bool, now time.Time) {
	g := signal.Gate
	if g == nil {
		return
	}
	observation := profileAlertObservation{Config: api.ProfileDeploymentPolicyConfig{Options: signal.Options}, Assessment: a}
	for _, r := range a.RouteChecks {
		if qualifiedProfileGateRoute(observation, r) {
			retry = false
			break
		}
	}
	if retry && signal.Attempts < api.ProfileAutoMaxAttempts && now.Before(g.Deadline) {
		return
	}
	g.NextCandidate = nil
	completed := now.UTC()
	signal.CompletedAt = &completed
	signal.NextAttemptAt = nil
	signal.Status = a.Status
	signal.Reason = a.Reason
	signal.Baseline, signal.Candidate = &a.Baseline, &a.Candidate
	if g.LastWindowEnd != nil && (!a.Candidate.End.After(*g.LastWindowEnd) || a.Candidate.Start.Before(*g.LastWindowEnd)) {
		g.Status, g.Reason = "inconclusive", "Repeated or overlapping profile windows cannot confirm a gate."
		return
	}
	end := a.Candidate.End
	g.LastWindowEnd = &end
	g.Windows++
	allHealthy := len(g.Streaks) > 0
	for i := range g.Streaks {
		streak := &g.Streaks[i]
		status := "insufficient_data"
		for _, r := range a.RouteChecks {
			if r.Route == streak.Route && qualifiedProfileGateRoute(observation, r) {
				status = r.Status
				break
			}
		}
		if status == "insufficient_data" {
			streak.Count = 0
		} else if status != streak.Status {
			streak.Count = 1
		} else if streak.Count < g.Policy.Confirmations {
			streak.Count++
		}
		streak.Status = status
		if status == "regressed" && streak.Count >= g.Policy.Confirmations {
			g.Status, g.Reason = "regressed", "Confirmed route CPU/request regression: "+streak.Route
			return
		}
		if status != "no_regression_detected" || streak.Count < g.Policy.Confirmations {
			allHealthy = false
		}
	}
	if allHealthy {
		g.Status, g.Reason = "passed", "Every configured route passed consecutive qualified CPU/request windows."
		return
	}
	next := a.Candidate
	next.Start = next.End
	next.End = next.Start.Add(time.Duration(signal.WindowSeconds) * time.Second)
	due := next.End.Add(api.ProfileAutoIngestionGrace)
	if !now.Before(g.Deadline) || due.After(g.Deadline) {
		g.Status, g.Reason = "timed_out", "Insufficient consecutive qualified profile evidence before the gate deadline."
		return
	}
	g.Status, g.Reason = "collecting", "Waiting for consecutive qualified route windows."
	g.NextCandidate = &next
	signal.Attempts = 0
	signal.Status, signal.Reason = "queued", g.Reason
	signal.NextAttemptAt, signal.CompletedAt = &due, nil
}

func decideProfileCanaryGate(d Deployment, p api.ProfileDeploymentPolicy, signal *api.CanaryProfileSignal, stableID string, stableCount int, now time.Time) api.ProfileCanaryGateDecision {
	out := api.ProfileCanaryGateDecision{Status: "disabled", Reason: "Profiling gate is not configured.", CanaryStep: d.CanaryStep, CanaryStepStartedAt: d.CanaryStepStartedAt, PolicyRevision: p.Revision}
	if !p.Config.Enabled || p.Config.CanaryGate == nil {
		return out
	}
	gate := p.Config.CanaryGate
	out.OnTimeout, out.AutoRollback = gate.OnTimeout, gate.AutoRollback
	out.StableDeploymentID = stableID
	out.Status, out.Reason = "collecting", "Waiting for qualified profile evidence for the current stage and policy."
	if d.CanaryStepStartedAt == nil {
		out.Reason = "The current stage has no observation anchor."
		return out
	}
	deadline := profileGateAnchor(d, p).Add(time.Duration(gate.TimeoutSeconds) * time.Second)
	out.Deadline = &deadline
	contextMatches := signal != nil && signal.Mode == "gate" && signal.Gate != nil && signal.PolicyRevision == p.Revision && signal.CanaryStep == d.CanaryStep && signal.CanaryStepStartedAt.Equal(*d.CanaryStepStartedAt) && signal.Candidate != nil && signal.Candidate.DeploymentID == d.ID && signal.Baseline != nil && signal.Baseline.DeploymentID == stableID && stableCount == 1
	if contextMatches {
		out.Signal = signal
		switch signal.Gate.Status {
		case "passed", "regressed":
			out.Status, out.Reason = signal.Gate.Status, signal.Gate.Reason
			return out
		}
	} else if stableCount != 1 {
		out.Reason = "A unique current stable predecessor is required for profile gating."
	}
	if !now.Before(deadline) {
		out.Status, out.Reason = "timed_out", "Profile evidence did not qualify for the current stage before its deadline."
	}
	return out
}

func authorizeProfileGate(decision api.ProfileCanaryGateDecision, params *CanaryAdvanceParams) error {
	if override := params.ProfileGateOverride; override != nil {
		if params.RequireSafeReleaseLease || params.ProfileGateRollback || override.ExpectedPolicyRevision != decision.PolicyRevision || len(strings.TrimSpace(override.Reason)) == 0 || !investigationText(override.Reason, api.ProfileGateOverrideMaxReasonBytes) {
			return fmt.Errorf("%w: profile gate override requires the current policy revision and a bounded reason from a customer request", ErrInvalidArgument)
		}
		decision.Status, decision.Reason = "overridden", strings.TrimSpace(override.Reason)
	}
	if params.ProfileGateDecision != nil {
		*params.ProfileGateDecision = decision
	}
	if params.ProfileGateRollback {
		if !params.RequireSafeReleaseLease || decision.Status != "regressed" || !decision.AutoRollback {
			return &ProfileGateBlockedError{Decision: decision}
		}
	} else if decision.Status != "disabled" && decision.Status != "passed" && decision.Status != "overridden" && !(decision.Status == "timed_out" && decision.OnTimeout == "continue") {
		return &ProfileGateBlockedError{Decision: decision}
	}
	var data map[string]any
	if len(params.Audit.Data) > 0 {
		if err := json.Unmarshal(params.Audit.Data, &data); err != nil {
			return err
		}
	}
	if data == nil {
		data = map[string]any{}
	}
	auditDecision := decision
	auditDecision.Signal = nil
	data["profile_gate"] = auditDecision
	if params.ProfileGateRollback {
		data["to_percent"] = 0
		data["to_step"] = decision.CanaryStep
		data["action"] = "profile_gate_rollback"
		params.Audit.Kind = DeployRolloutAborted
	}
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode profile gate audit: %w", err)
	}
	params.Audit.Data = body
	return nil
}

func legacyProfileCanaryGate(p api.ProfileDeploymentPolicy, d Deployment) error {
	if !p.Config.Enabled || p.Config.CanaryGate == nil {
		return nil
	}
	return &ProfileGateBlockedError{Decision: api.ProfileCanaryGateDecision{Status: "collecting", PolicyRevision: p.Revision, CanaryStep: d.CanaryStep, Reason: "Use the canary stage advance API to evaluate or explicitly override the profiling gate; legacy recovery cannot bypass it."}}
}

func qualifiedProfileGateRoute(o profileAlertObservation, r api.ProfileRouteRegression) bool {
	if !profileAlertEligible(o, r) {
		return false
	}
	for i, c := range []*api.ProfileCoverage{o.Assessment.BaselineCoverage, o.Assessment.CandidateCoverage} {
		query := o.Assessment.Baseline
		if i == 1 {
			query = o.Assessment.Candidate
		}
		seconds := query.End.Sub(query.Start).Seconds()
		if c.ContributingCollectors < 1 || seconds <= 0 || math.Abs(c.WindowSeconds-seconds) > 1e-6 || c.CoveredSeconds > seconds+1e-6 {
			return false
		}
	}
	return r.Status == "regressed" || r.Status == "no_regression_detected"
}
