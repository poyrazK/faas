package main

import (
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentStability struct {
	RequiredSeconds   float64                            `json:"required_seconds"`
	State             string                             `json:"state"`
	Reason            string                             `json:"reason,omitempty"`
	WindowStartedAt   *time.Time                         `json:"window_started_at"`
	LastObservedAt    *time.Time                         `json:"last_observed_at"`
	ObservedSeconds   float64                            `json:"observed_seconds"`
	ElapsedSeconds    float64                            `json:"elapsed_seconds"`
	SourceSpanSeconds float64                            `json:"source_span_seconds"`
	SourceCount       int                                `json:"source_count"`
	Resets            int                                `json:"resets"`
	LastResetAt       *time.Time                         `json:"last_reset_at"`
	LastResetReason   string                             `json:"last_reset_reason,omitempty"`
	Diagnostics       *obsDeploymentStabilityDiagnostics `json:"diagnostics,omitempty"`
}

type obsDeploymentStabilityRecorder struct {
	required              time.Duration
	started, lastObserved time.Time
	first, last           obsDeploymentStabilityEvidence
	blockers              *obsDeploymentStabilityPreview
	status                obsDeploymentStability
}

func newObsDeploymentStability(required time.Duration) *obsDeploymentStabilityRecorder {
	return &obsDeploymentStabilityRecorder{required: required, status: obsDeploymentStability{RequiredSeconds: required.Seconds(), State: "waiting", Reason: "awaiting_passing_checks", Diagnostics: &obsDeploymentStabilityDiagnostics{State: "not_observed", ObservationRemainingSeconds: required.Seconds()}}}
}

func (r *obsDeploymentStabilityRecorder) observe(state string, serving *api.ObsServingHealth, notifications *obsDeploymentNotifications, at time.Time) bool {
	previous := r.lastObserved
	r.lastObserved = at
	r.status.Diagnostics = &obsDeploymentStabilityDiagnostics{State: "unavailable", ObservationRemainingSeconds: r.required.Seconds(), LastReset: r.status.Diagnostics.LastReset}
	observed := at.UTC()
	r.status.LastObservedAt = &observed
	if state != "passed" {
		reason := "checks_unknown"
		if state == "degraded" {
			reason = "checks_degraded"
		}
		r.reset(reason, at, previous, nil)
		return false
	}
	evidence, valid := collectObsDeploymentStabilityEvidence(serving, notifications)
	if !valid {
		r.reset("evidence_unavailable", at, previous, nil)
		return false
	}
	if !r.started.IsZero() {
		reason := ""
		switch {
		case at.Before(previous):
			reason = "observation_clock_regressed"
		case at.Sub(previous) > api.ObsHealthFreshnessMaxAge:
			reason = "observation_gap"
		case !sameObsDeploymentStabilitySources(r.last, evidence):
			reason = "evidence_set_changed"
		case regressedObsDeploymentStabilityEvidence(r.last, evidence):
			reason = "evidence_clock_regressed"
		}
		if reason != "" {
			r.reset(reason, at, previous, evidence)
		}
	}
	if r.started.IsZero() {
		r.started, r.first = at, evidence
		start := at.UTC()
		r.status.WindowStartedAt = &start
	}
	r.status.SourceCount = len(evidence)
	elapsed := max(time.Duration(0), at.Sub(r.started))
	span := obsDeploymentStabilitySpan(r.first, evidence)
	r.status.Diagnostics.State = "available"
	r.status.Diagnostics.ObservationRemainingSeconds = max(time.Duration(0), r.required-elapsed).Seconds()
	r.status.Diagnostics.Blockers = obsDeploymentStabilityBlockers(r.first, r.last, evidence, r.required)
	r.blockers = r.status.Diagnostics.Blockers
	r.last = evidence
	progress := min(elapsed, span)
	r.status.ElapsedSeconds, r.status.SourceSpanSeconds, r.status.ObservedSeconds = elapsed.Seconds(), span.Seconds(), progress.Seconds()
	r.status.State, r.status.Reason = "stabilizing", "awaiting_fresh_evidence"
	if span >= r.required && elapsed < r.required {
		r.status.Reason = "awaiting_observation_window"
	}
	if progress >= r.required {
		r.status.State, r.status.Reason = "passed", ""
		return true
	}
	return false
}

func (r *obsDeploymentStabilityRecorder) reset(reason string, at, previous time.Time, current obsDeploymentStabilityEvidence) {
	if !r.started.IsZero() {
		r.status.Diagnostics.LastReset = obsDeploymentStabilityResetDetails(reason, at, previous, r.first, r.last, current, r.blockers)
		r.status.Resets++
		reset := at.UTC()
		r.status.LastResetAt, r.status.LastResetReason = &reset, reason
	}
	r.started, r.first, r.last = time.Time{}, nil, nil
	r.blockers = nil
	r.status.State, r.status.Reason, r.status.WindowStartedAt = "waiting", reason, nil
	r.status.ElapsedSeconds, r.status.SourceSpanSeconds, r.status.ObservedSeconds, r.status.SourceCount = 0, 0, 0, 0
}

func (r *obsDeploymentStabilityRecorder) snapshot() *obsDeploymentStability {
	out := r.status
	out.Diagnostics = cloneObsDeploymentStabilityDiagnostics(r.status.Diagnostics)
	return &out
}

func sameObsDeploymentStabilitySources(a, b obsDeploymentStabilityEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, exists := b[key]; !exists {
			return false
		}
	}
	return true
}

func regressedObsDeploymentStabilityEvidence(previous, current obsDeploymentStabilityEvidence) bool {
	for key, at := range current {
		if at.Before(previous[key]) {
			return true
		}
	}
	return false
}

func obsDeploymentStabilitySpan(first, current obsDeploymentStabilityEvidence) time.Duration {
	var span time.Duration
	started := false
	for key, at := range current {
		delta := max(time.Duration(0), at.Sub(first[key]))
		if !started || delta < span {
			span, started = delta, true
		}
	}
	return span
}

func terminalObsDeploymentStability(report obsDeploymentReport, state, reason string) obsDeploymentReport {
	if report.Stability != nil {
		stability := *report.Stability
		stability.State = state
		if reason != "" {
			stability.Reason = reason
		}
		report.Stability = &stability
	}
	return report
}

func expireObsDeploymentStability(report obsDeploymentReport) obsDeploymentReport {
	if report.Stability != nil && report.Stability.State != "passed" {
		if report.Reason == "awaiting_stability" {
			report.Reason = "stability_window_incomplete"
		}
		return terminalObsDeploymentStability(report, "incomplete", "")
	}
	return report
}

func writeObsDeploymentStability(w io.Writer, stability *obsDeploymentStability) {
	if stability == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "stability state=%s reason=%s required_seconds=%g observed_seconds=%g elapsed_seconds=%g source_span_seconds=%g source_count=%d window_started_at=%s last_observed_at=%s resets=%d last_reset_at=%s last_reset_reason=%s\n", stability.State, stability.Reason, stability.RequiredSeconds, stability.ObservedSeconds, stability.ElapsedSeconds, stability.SourceSpanSeconds, stability.SourceCount, servingTime(stability.WindowStartedAt), servingTime(stability.LastObservedAt), stability.Resets, servingTime(stability.LastResetAt), stability.LastResetReason)
	writeObsDeploymentStabilityDiagnostics(w, stability.Diagnostics)
}
