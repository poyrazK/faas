package main

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentServingRead struct {
	value  api.ObsServingHealth
	err    error
	timing *obsDeploymentReadTiming
}
type obsDeploymentSnapshot struct {
	serving            obsDeploymentServingRead
	notifications      api.ObsNotificationHealth
	notificationErr    error
	notificationTiming *obsDeploymentReadTiming
}
type obsDeploymentCheck struct {
	Name             string `json:"name"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	Runbook          string `json:"runbook"`
	SourceHTTPStatus *int   `json:"source_http_status,omitempty"`
}
type obsDeploymentNotifications struct {
	GeneratedAt     time.Time                            `json:"generated_at"`
	AlertmanagerAPI api.ObsNotificationAPIStatus         `json:"alertmanager_api"`
	Signals         map[string]api.ObsNotificationSignal `json:"signals"`
	Integrations    []api.ObsNotificationIntegration     `json:"integrations"`
	ReceiverHealth  *api.ObsNotificationReceiverHealth   `json:"receiver_health,omitempty"`
}
type obsDeploymentReport struct {
	SchemaVersion         int                         `json:"schema_version"`
	Scope                 string                      `json:"scope"`
	Release               string                      `json:"requested_release"`
	After                 time.Time                   `json:"evidence_after"`
	StartedAt             time.Time                   `json:"started_at"`
	UpdatedAt             time.Time                   `json:"updated_at"`
	WaitSeconds           float64                     `json:"wait_seconds"`
	Attempts              int                         `json:"attempts"`
	State                 string                      `json:"state"`
	Reason                string                      `json:"reason"`
	HadConfirmedFaults    bool                        `json:"had_confirmed_faults"`
	Checks                []obsDeploymentCheck        `json:"checks"`
	Timeline              *obsDeploymentTimeline      `json:"timeline,omitempty"`
	Serving               *api.ObsServingHealth       `json:"serving,omitempty"`
	Notifications         *obsDeploymentNotifications `json:"notifications,omitempty"`
	Limitations           []string                    `json:"limitations"`
	Stability             *obsDeploymentStability     `json:"stability,omitempty"`
	Sources               *obsDeploymentSources       `json:"sources,omitempty"`
	QualificationEvidence *obsDeploymentEvidence      `json:"qualification_evidence,omitempty"`
}

func runObsDeploymentVerification(ctx context.Context, opts obsDeploymentOptions, fetch func(context.Context) obsDeploymentSnapshot, now func() time.Time, wait func(context.Context, time.Duration) error, save func(obsDeploymentReport) error) (obsDeploymentReport, int) {
	started := now().UTC()
	budget := opts.wait
	if budget == 0 {
		budget = api.ObsPostDeployWaitDefault
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsPostDeployWaitMax)
	defer cancel()
	checks := []obsDeploymentCheck{obsDeploymentCheckFor("serving_probes"), obsDeploymentCheckFor("notification_pipeline"), obsDeploymentCheckFor("notification_receivers")}
	timeline := newObsDeploymentTimeline(checks)
	timeline.notifications = newObsDeploymentNotificationRecorder(opts.after)
	sources := &obsDeploymentSourceRecorder{}
	evidence := newObsDeploymentEvidence(checks)
	report := obsDeploymentReport{SchemaVersion: 1, Scope: "post_deployment_monitoring", Release: opts.release, After: opts.after, StartedAt: started, UpdatedAt: started, WaitSeconds: budget.Seconds(), State: "unknown", Reason: "awaiting_evidence", Checks: checks, Timeline: timeline.snapshot(), Limitations: []string{"requested_release_not_runtime_identity", "notification_windows_can_include_pre_deployment_events", "success_clock_is_collector_confirmation", "no_notification_sent_or_human_receipt_proven", "no_deployment_or_rollback_performed", "poll_intervals_are_duration_estimates", "direct_notification_history_advisory_not_qualification", obsDeploymentNotificationPreviewLimitation}}
	report.Sources = sources.snapshot()
	report.QualificationEvidence = evidence.snapshot(nil)
	var stability *obsDeploymentStabilityRecorder
	if opts.stableFor > 0 {
		stability = newObsDeploymentStability(opts.stableFor)
		report.Stability = stability.snapshot()
		report.Limitations = append(report.Limitations, "stability_is_sampled_evidence_not_continuous_availability")
	}
	// Replace a previous run's passing file before starting network reads.
	report = fitObsDeploymentReport(report)
	if err := save(report); err != nil {
		report.Reason = "report_write_failed"
		return terminalObsDeploymentStability(report, "interrupted", "report_write_failed"), 2
	}
	for {
		if ctx.Err() != nil {
			return cancelObsDeploymentReport(report, now(), save)
		}
		deadline := started.Add(budget)
		readCtx, readCancel := context.WithDeadline(ctx, deadline)
		snapshot := fetch(readCtx)
		readCancel()
		report.Attempts++
		observed := now()
		report.UpdatedAt = observed.UTC()
		report.Checks, report.Serving, report.Notifications = assessObsDeployment(snapshot, opts.after, report.UpdatedAt)
		report.Sources = sources.observe(snapshot, report.Serving != nil, report.Notifications != nil, observed)
		report.QualificationEvidence = evidence.observe(report.Checks, snapshot, report.Serving, report.Notifications, opts.after, observed)
		timeline.notifications.observe(report.Serving, observed, report.Attempts)
		failures := obsDeploymentProbeFailures(report.Checks, report.Serving, opts.after, report.UpdatedAt)
		notificationFailures := obsDeploymentNotificationFailures(report.Checks, report.Notifications, opts.after, report.UpdatedAt)
		report.Timeline = timeline.observeWithFaults(report.Checks, observed, report.Attempts, failures, notificationFailures)
		report.State, report.Reason = obsDeploymentOverall(report.Checks)
		if report.State == "degraded" {
			report.HadConfirmedFaults = true
		}
		if stability != nil {
			passed := stability.observeChecks(report.Checks, report.Serving, report.Notifications, observed)
			report.Stability = stability.snapshot()
			if report.State == "passed" && !passed {
				report.State, report.Reason = "unknown", "awaiting_stability"
			}
		}
		if opts.wait == 0 && report.Serving != nil {
			budget = obsDeploymentPolicyWait(*report.Serving)
		}
		report.WaitSeconds = budget.Seconds()
		if stability != nil && report.State == "passed" && observed.After(started.Add(budget)) {
			// A late read can retain healthy evidence, but cannot complete a
			// stability window outside the wait budget.
			report.State, report.Reason = "unknown", "awaiting_stability"
			report = terminalObsDeploymentStability(report, "incomplete", "observed_after_deadline")
		}
		if ctx.Err() != nil {
			return cancelObsDeploymentReport(report, now(), save)
		}
		if !now().Before(started.Add(budget)) {
			report = expireObsDeploymentStability(report)
		}
		report = fitObsDeploymentReport(report)
		if err := save(report); err != nil {
			report.State, report.Reason = "unknown", "report_write_failed"
			report = terminalObsDeploymentStability(report, "interrupted", "report_write_failed")
			return fitObsDeploymentReport(report), 2
		}
		if report.State == "passed" {
			return report, 0
		}
		if !now().Before(started.Add(budget)) {
			if report.Stability != nil && report.Stability.State != "incomplete" {
				// A write can cross the deadline. Persist the terminal summary
				// without inventing another source observation or network poll.
				report.UpdatedAt = now().UTC()
				report = fitObsDeploymentReport(expireObsDeploymentStability(report))
				if err := save(report); err != nil {
					report.State, report.Reason = "unknown", "report_write_failed"
					return fitObsDeploymentReport(terminalObsDeploymentStability(report, "interrupted", "report_write_failed")), 2
				}
			}
			if report.State == "degraded" {
				return report, 1
			}
			return report, 3
		}
		pause := min(opts.poll, started.Add(budget).Sub(now()))
		if err := wait(ctx, pause); err != nil {
			return cancelObsDeploymentReport(report, now(), save)
		}
	}
}

func cancelObsDeploymentReport(report obsDeploymentReport, now time.Time, save func(obsDeploymentReport) error) (obsDeploymentReport, int) {
	report.State, report.Reason, report.UpdatedAt = "unknown", "cancelled", now.UTC()
	report = terminalObsDeploymentStability(report, "interrupted", "cancelled")
	report = fitObsDeploymentReport(report)
	if err := save(report); err != nil {
		report.Reason = "report_write_failed"
		return fitObsDeploymentReport(terminalObsDeploymentStability(report, "interrupted", "report_write_failed")), 2
	}
	return report, 3
}

func obsDeploymentPolicyWait(serving api.ObsServingHealth) time.Duration {
	seconds := 0.0
	for _, probe := range serving.Probes {
		seconds = math.Max(seconds, probe.MaxResultAgeSeconds)
	}
	if seconds <= 0 {
		return api.ObsPostDeployWaitDefault
	}
	seconds = math.Min(seconds+api.ObsHealthFreshnessMaxAge.Seconds(), api.ObsPostDeployWaitMax.Seconds())
	return time.Duration(seconds * float64(time.Second))
}

func obsDeploymentOverall(checks []obsDeploymentCheck) (string, string) {
	state := "passed"
	for _, check := range checks {
		if check.State == "degraded" {
			return "degraded", "confirmed_monitoring_fault"
		}
		if check.State != "passed" {
			state = "unknown"
		}
	}
	if len(checks) != 3 || state == "unknown" {
		return "unknown", "incomplete_monitoring_evidence"
	}
	return "passed", ""
}

func obsDeploymentFresh(at *time.Time, after, now time.Time) bool {
	return at != nil && !at.IsZero() && !at.Before(after) && !at.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) && !at.After(now.Add(api.ObsHealthClockSkewTolerance))
}
