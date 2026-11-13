package main

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentServingRead struct {
	value api.ObsServingHealth
	err   error
}
type obsDeploymentSnapshot struct {
	serving         obsDeploymentServingRead
	notifications   api.ObsNotificationHealth
	notificationErr error
}
type obsDeploymentCheck struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Runbook string `json:"runbook"`
}
type obsDeploymentNotifications struct {
	GeneratedAt     time.Time                            `json:"generated_at"`
	AlertmanagerAPI api.ObsNotificationAPIStatus         `json:"alertmanager_api"`
	Signals         map[string]api.ObsNotificationSignal `json:"signals"`
	Integrations    []api.ObsNotificationIntegration     `json:"integrations"`
	ReceiverHealth  *api.ObsNotificationReceiverHealth   `json:"receiver_health,omitempty"`
}
type obsDeploymentReport struct {
	SchemaVersion      int                         `json:"schema_version"`
	Scope              string                      `json:"scope"`
	Release            string                      `json:"requested_release"`
	After              time.Time                   `json:"evidence_after"`
	StartedAt          time.Time                   `json:"started_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	WaitSeconds        float64                     `json:"wait_seconds"`
	Attempts           int                         `json:"attempts"`
	State              string                      `json:"state"`
	Reason             string                      `json:"reason"`
	HadConfirmedFaults bool                        `json:"had_confirmed_faults"`
	Checks             []obsDeploymentCheck        `json:"checks"`
	Serving            *api.ObsServingHealth       `json:"serving,omitempty"`
	Notifications      *obsDeploymentNotifications `json:"notifications,omitempty"`
	Limitations        []string                    `json:"limitations"`
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
	report := obsDeploymentReport{SchemaVersion: 1, Scope: "post_deployment_monitoring", Release: opts.release, After: opts.after, StartedAt: started, UpdatedAt: started, WaitSeconds: budget.Seconds(), State: "unknown", Reason: "awaiting_evidence", Checks: checks, Limitations: []string{"requested_release_not_runtime_identity", "notification_windows_can_include_pre_deployment_events", "success_clock_is_collector_confirmation", "no_notification_sent_or_human_receipt_proven", "no_deployment_or_rollback_performed"}}
	// Replace a previous run's passing file before starting network reads.
	if err := save(report); err != nil {
		report.Reason = "report_write_failed"
		return report, 2
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
		report.UpdatedAt = now().UTC()
		report.Checks, report.Serving, report.Notifications = assessObsDeployment(snapshot, opts.after, report.UpdatedAt)
		report.State, report.Reason = obsDeploymentOverall(report.Checks)
		if report.State == "degraded" {
			report.HadConfirmedFaults = true
		}
		if opts.wait == 0 && report.Serving != nil {
			budget = obsDeploymentPolicyWait(*report.Serving)
		}
		report.WaitSeconds = budget.Seconds()
		if ctx.Err() != nil {
			return cancelObsDeploymentReport(report, now(), save)
		}
		if err := save(report); err != nil {
			report.State, report.Reason = "unknown", "report_write_failed"
			return report, 2
		}
		if report.State == "passed" {
			return report, 0
		}
		if !now().Before(started.Add(budget)) {
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
	if err := save(report); err != nil {
		report.Reason = "report_write_failed"
		return report, 2
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
