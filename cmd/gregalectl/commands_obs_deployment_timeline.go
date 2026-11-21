package main

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentTransition struct {
	At                   time.Time                                 `json:"at"`
	Attempt              int                                       `json:"attempt"`
	Check                string                                    `json:"check"`
	State                string                                    `json:"state"`
	Reason               string                                    `json:"reason"`
	SourceHTTPStatus     *int                                      `json:"source_http_status,omitempty"`
	ProbeFailures        *obsDeploymentProbeFailureSnapshot        `json:"probe_failures,omitempty"`
	NotificationFailures *obsDeploymentNotificationFailureSnapshot `json:"notification_failures,omitempty"`
}

type obsDeploymentCheckTimeline struct {
	Name                 string     `json:"name"`
	LastObservedAt       *time.Time `json:"last_observed_at,omitempty"`
	FirstFaultAt         *time.Time `json:"first_fault_at,omitempty"`
	FirstFaultReason     string     `json:"first_fault_reason,omitempty"`
	LastRecoveryAt       *time.Time `json:"last_recovery_at,omitempty"`
	ObservedFaultSeconds float64    `json:"observed_fault_seconds"`
	UnrecoveredFault     bool       `json:"unrecovered_fault"`
}

type obsDeploymentTimeline struct {
	TransitionLimit      int                                  `json:"transition_limit"`
	DroppedTransitions   int                                  `json:"dropped_transitions"`
	Truncated            bool                                 `json:"truncated"`
	Transitions          []obsDeploymentTransition            `json:"transitions"`
	Checks               []obsDeploymentCheckTimeline         `json:"checks"`
	ProbeEvidence        *obsDeploymentProbeEvidenceRetention `json:"probe_evidence,omitempty"`
	NotificationEvidence *obsDeploymentProbeEvidenceRetention `json:"notification_evidence,omitempty"`
	ProbeNotifications   *obsDeploymentNotificationHistory    `json:"probe_notifications,omitempty"`
}

type obsDeploymentCheckObservation struct {
	summary obsDeploymentCheckTimeline
	check   obsDeploymentCheck
	at      time.Time
	seen    bool
	cause   string
}

type obsDeploymentTimelineRecorder struct {
	transitions          []obsDeploymentTransition
	dropped              int
	checks               []obsDeploymentCheckObservation
	evidence             obsDeploymentProbeEvidenceRetention
	notificationEvidence obsDeploymentProbeEvidenceRetention
	notifications        *obsDeploymentNotificationRecorder
}

func newObsDeploymentTimeline(checks []obsDeploymentCheck) *obsDeploymentTimelineRecorder {
	r := &obsDeploymentTimelineRecorder{transitions: []obsDeploymentTransition{}, evidence: obsDeploymentProbeEvidenceRetention{PerTransitionLimit: api.ObsPostDeployProbeEvidencePerTransitionMax, ItemLimit: api.ObsPostDeployProbeEvidenceItemsMax, ByteLimit: api.ObsPostDeployProbeEvidenceBytesMax}}
	r.notificationEvidence = obsDeploymentProbeEvidenceRetention{PerTransitionLimit: api.ObsPostDeployNotificationEvidencePerTransitionMax, ItemLimit: api.ObsPostDeployNotificationEvidenceItemsMax, ByteLimit: api.ObsPostDeployNotificationEvidenceBytesMax}
	for _, check := range checks {
		r.checks = append(r.checks, obsDeploymentCheckObservation{summary: obsDeploymentCheckTimeline{Name: check.Name}})
	}
	return r
}

func (r *obsDeploymentTimelineRecorder) observe(checks []obsDeploymentCheck, at time.Time, attempt int) *obsDeploymentTimeline {
	return r.observeWithProbeFailures(checks, at, attempt, obsDeploymentProbeFailureSet{})
}

func (r *obsDeploymentTimelineRecorder) observeWithProbeFailures(checks []obsDeploymentCheck, at time.Time, attempt int, failures obsDeploymentProbeFailureSet) *obsDeploymentTimeline {
	return r.observeWithFaults(checks, at, attempt, failures, nil)
}

func (r *obsDeploymentTimelineRecorder) observeWithFaults(checks []obsDeploymentCheck, at time.Time, attempt int, failures obsDeploymentProbeFailureSet, notifications map[string]obsDeploymentNotificationFailureSet) *obsDeploymentTimeline {
	for _, check := range checks {
		for i := range r.checks {
			if r.checks[i].summary.Name == check.Name {
				r.observeCheck(&r.checks[i], check, at, attempt, failures, notifications[check.Name])
				break
			}
		}
	}
	return r.snapshot()
}

func (r *obsDeploymentTimelineRecorder) observeCheck(previous *obsDeploymentCheckObservation, check obsDeploymentCheck, at time.Time, attempt int, failures obsDeploymentProbeFailureSet, notifications obsDeploymentNotificationFailureSet) {
	observed := at.UTC()
	summary := &previous.summary
	// Estimate intervals using the last observed state, stopping at an unknown
	// observation. Raw times retain Go's monotonic clock for elapsed durations.
	if previous.seen && previous.check.State == "degraded" {
		summary.ObservedFaultSeconds += max(0, at.Sub(previous.at).Seconds())
	}
	if check.State == "degraded" {
		if summary.FirstFaultAt == nil {
			summary.FirstFaultAt, summary.FirstFaultReason = &observed, check.Reason
		}
		summary.UnrecoveredFault = true
	} else if check.State == "passed" && summary.UnrecoveredFault {
		summary.LastRecoveryAt, summary.UnrecoveredFault = &observed, false
	}
	if check.Name != "serving_probes" || check.State != "degraded" {
		failures = obsDeploymentProbeFailureSet{}
	}
	if check.State != "degraded" || check.Name != "notification_pipeline" && check.Name != "notification_receivers" {
		notifications = obsDeploymentNotificationFailureSet{}
	}
	cause := failures.fingerprint + notifications.fingerprint
	if !previous.seen || previous.check.State != check.State || previous.check.Reason != check.Reason || previous.cause != cause || !sameObsDeploymentSourceStatus(previous.check.SourceHTTPStatus, check.SourceHTTPStatus) {
		r.appendTransition(obsDeploymentTransition{At: observed, Attempt: attempt, Check: check.Name, State: check.State, Reason: check.Reason, SourceHTTPStatus: obsDeploymentEvidencePtr(check.SourceHTTPStatus)}, failures, notifications)
	}
	summary.LastObservedAt = &observed
	previous.check, previous.at, previous.seen = check, at, true
	previous.check.SourceHTTPStatus = obsDeploymentEvidencePtr(check.SourceHTTPStatus)
	previous.cause = cause
}

func (r *obsDeploymentTimelineRecorder) appendTransition(transition obsDeploymentTransition, failures obsDeploymentProbeFailureSet, notifications obsDeploymentNotificationFailureSet) {
	if len(r.transitions) == api.ObsPostDeployTransitionsMax {
		old := r.transitions[0]
		if old.ProbeFailures != nil {
			r.evidence.RetainedItems -= len(old.ProbeFailures.Items)
			r.evidence.RetainedBytes -= obsDeploymentProbeEvidenceBytes(old, old.ProbeFailures)
			r.evidence.DroppedItems += len(old.ProbeFailures.Items)
			r.evidence.DroppedSnapshots++
		}
		if old.NotificationFailures != nil {
			r.notificationEvidence.RetainedItems -= len(old.NotificationFailures.Items)
			r.notificationEvidence.RetainedBytes -= obsDeploymentNotificationEvidenceBytes(old, old.NotificationFailures)
			r.notificationEvidence.DroppedItems += len(old.NotificationFailures.Items)
			r.notificationEvidence.DroppedSnapshots++
		}
		copy(r.transitions, r.transitions[1:])
		r.transitions = r.transitions[:len(r.transitions)-1]
		r.dropped++
	}
	transition.ProbeFailures = r.retainProbeFailures(transition, failures)
	transition.NotificationFailures = r.retainNotificationFailures(transition, notifications)
	r.transitions = append(r.transitions, transition)
}

func (r *obsDeploymentTimelineRecorder) snapshot() *obsDeploymentTimeline {
	checks := make([]obsDeploymentCheckTimeline, len(r.checks))
	for i, check := range r.checks {
		checks[i] = check.summary
	}
	// Saving a later poll must not mutate an earlier snapshot, even when the
	// retained transition buffer rolls over. Timestamp pointers are immutable.
	evidence := r.evidence
	evidence.Truncated = evidence.DroppedItems > 0 || evidence.DroppedSnapshots > 0
	out := &obsDeploymentTimeline{TransitionLimit: api.ObsPostDeployTransitionsMax, DroppedTransitions: r.dropped, Truncated: r.dropped > 0, Transitions: slices.Clone(r.transitions), Checks: checks, ProbeEvidence: &evidence}
	notificationEvidence := r.notificationEvidence
	notificationEvidence.Truncated = notificationEvidence.DroppedItems > 0 || notificationEvidence.DroppedSnapshots > 0
	out.NotificationEvidence = &notificationEvidence
	if r.notifications != nil {
		out.ProbeNotifications = r.notifications.snapshot()
	}
	return out
}

func writeObsDeploymentHuman(w io.Writer, report obsDeploymentReport) {
	_, _ = fmt.Fprintf(w, "monitoring=%s reason=%s release=%s attempts=%d wait_seconds=%g had_confirmed_faults=%t\n", report.State, report.Reason, report.Release, report.Attempts, report.WaitSeconds, report.HadConfirmedFaults)
	writeObsDeploymentSources(w, report.Sources)
	writeObsDeploymentRuntimeBuilds(w, report.RuntimeBuilds)
	writeObsDeploymentEvidence(w, report.QualificationEvidence)
	writeObsDeploymentStability(w, report.Stability)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "  check=%s state=%s reason=%s runbook=%s", check.Name, check.State, check.Reason, check.Runbook)
		if check.SourceHTTPStatus != nil {
			_, _ = fmt.Fprintf(w, " source_http_status=%d", *check.SourceHTTPStatus)
		}
		_, _ = fmt.Fprintln(w)
	}
	if report.Timeline == nil {
		return
	}
	history := report.Timeline
	_, _ = fmt.Fprintf(w, "timeline transitions=%d dropped_transitions=%d truncated=%t\n", len(history.Transitions), history.DroppedTransitions, history.Truncated)
	for _, check := range history.Checks {
		if check.FirstFaultAt != nil {
			_, _ = fmt.Fprintf(w, "  check=%s first_fault_at=%s first_fault_reason=%s last_recovery_at=%s observed_fault_seconds=%g unrecovered_fault=%t\n", check.Name, obsDeploymentTimelineTime(check.FirstFaultAt), check.FirstFaultReason, obsDeploymentTimelineTime(check.LastRecoveryAt), check.ObservedFaultSeconds, check.UnrecoveredFault)
		}
	}
	writeObsDeploymentSourceTransitions(w, history.Transitions)
	writeObsDeploymentProbeEvidence(w, history)
	writeObsDeploymentNotificationEvidence(w, history)
	writeObsDeploymentNotificationHistory(w, history.ProbeNotifications)
	if history.ProbeNotifications == nil && slices.Contains(report.Limitations, obsDeploymentNotificationEnvelopeLimitation) {
		_, _ = fmt.Fprintln(w, "probe_notification_history advisory=true unassessed=true reason=report_envelope")
	}
}

func obsDeploymentTimelineTime(at *time.Time) string {
	if at == nil {
		return "unknown"
	}
	return at.UTC().Format(time.RFC3339Nano)
}
