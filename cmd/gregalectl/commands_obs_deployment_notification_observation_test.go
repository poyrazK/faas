package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestObsDeploymentNotificationHistoryUnavailableAndUnsafeSources(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	for _, tc := range []struct {
		name, reason string
		change       func() *api.ObsServingHealth
	}{
		{"failed serving read", "serving_snapshot_unavailable", func() *api.ObsServingHealth { return nil }},
		{"older server", "server_does_not_report_probe_notifications", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(now, "fsn-2", "unknown", time.Time{})
			out.ProbeNotifications = nil
			return &out
		}},
		{"unsafe detail", "metadata_inconsistent", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(now, "fsn-2", "unknown", time.Time{})
			out.ProbeNotifications.Channels[0].EvidenceReason = "https://private-provider/token"
			return &out
		}},
		{"unsafe identity", "metadata_inconsistent", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
			out.ProbeNotifications.Channels[0].Node = "https://private-provider/token"
			return &out
		}},
		{"old snapshot", "awaiting_post_deployment_evidence", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(now.Add(-121*time.Second), "fsn-2", "unknown", time.Time{})
			return &out
		}},
		{"before boundary", "awaiting_post_deployment_evidence", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(after.Add(-time.Second), "fsn-2", "unknown", time.Time{})
			return &out
		}},
		{"unavailable inventory", "inventory_unavailable", func() *api.ObsServingHealth {
			out := deploymentNotificationHistoryFixture(now, "fsn-2", "unknown", time.Time{})
			out.Inventory = api.ObsServingProbeInventory{State: "unavailable", Reason: "inventory_missing"}
			out.ProbeNotifications = operatorobs.UnavailableServingProbeNotifications("inventory_unavailable")
			return &out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := newObsDeploymentNotificationRecorder(after)
			failed := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
			recorder.observe(&failed, now, 1)
			recorder.observe(tc.change(), now, 2)
			history := recorder.snapshot()
			if !history.HadSubmissionFailures || history.UnresolvedTrackedFailures != 1 || history.SourceState != "unknown" || history.SourceReason != tc.reason || history.IncompleteObservations != 1 {
				t.Fatalf("source loss manufactured recovery or hid its cause: %+v", history)
			}
			last := history.Events[len(history.Events)-1]
			if last.State != "unobserved" || last.Reason != tc.reason || last.LastAttemptAt != nil || last.LastSuccessAt != nil || last.LaterSuccessAfterFailure {
				t.Fatal("missing evidence retained current history or supplied a success")
			}
			body, _ := json.Marshal(history)
			if strings.Contains(string(body), "private-provider") || strings.Contains(string(body), "https://") {
				t.Fatal("untrusted metadata entered notification history")
			}
		})
	}
}

func TestObsDeploymentNotificationHistoryNeedsCurrentPostBoundaryEvaluation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	serving := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
	old := after.Add(-time.Second)
	for i := range serving.ProbeNotifications.Channels {
		serving.ProbeNotifications.Channels[i].EvaluatedAt, serving.ProbeNotifications.Channels[i].LastRunAt = &old, &old
	}
	if !validObsServingHealth(serving) {
		t.Fatal("invalid pre-boundary evaluation fixture")
	}
	recorder := newObsDeploymentNotificationRecorder(after)
	recorder.observe(&serving, now, 1)
	history := recorder.snapshot()
	if history.HadSubmissionFailures || history.IncompleteObservations != 1 || history.Events[0].State != "unobserved" || history.Events[0].Reason != "awaiting_post_deployment_evidence" || history.Events[0].EvaluatedAt != nil {
		t.Fatal("pre-boundary evaluation became a current recorded failure")
	}
	// A response can cross the recording's freshness limit while in transit.
	serving = deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
	old = now.Add(-119 * time.Second)
	for i := range serving.ProbeNotifications.Channels {
		serving.ProbeNotifications.Channels[i].EvaluatedAt, serving.ProbeNotifications.Channels[i].LastRunAt = &old, &old
	}
	if !validObsServingHealth(serving) {
		t.Fatal("invalid in-transit freshness fixture")
	}
	recorder = newObsDeploymentNotificationRecorder(now.Add(-time.Hour))
	recorder.observe(&serving, now.Add(2*time.Second), 1)
	if history := recorder.snapshot(); history.HadSubmissionFailures || len(history.Events) != 0 || history.SourceReason != "notification_snapshot_not_current" || history.IncompleteObservations != 1 {
		t.Fatal("a stale source clock was refreshed by the verifier's observation")
	}
}

func TestObsDeploymentNotificationHistoryFailureAttemptsAndClockOnlyRefreshes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	completed := now.Add(-time.Hour)
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	for i := range 3 {
		serving := deploymentNotificationHistoryFixture(now.Add(time.Duration(i)*time.Second), "fsn-2", "last_submission_failed", completed)
		recorder.observe(&serving, serving.GeneratedAt, i+1)
	}
	if len(recorder.snapshot().Events) != 2 {
		t.Fatal("recording and run clocks alone filled notification history")
	}
	next := deploymentNotificationHistoryFixture(now.Add(3*time.Second), "fsn-2", "last_submission_failed", completed.Add(time.Second))
	recorder.observe(&next, next.GeneratedAt, 4)
	history := recorder.snapshot()
	if len(history.Events) != 3 || history.Events[2].FailureCode != "timeout" || !history.Events[2].LastAttemptAt.Equal(completed.Add(time.Second)) {
		t.Fatal("a genuinely newer failed submission was lost")
	}
}

func TestObsDeploymentNotificationHistoryCompletePreviewCanRetireNodeWithoutRecovery(t *testing.T) {
	now := time.Now().UTC()
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	failed := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
	recorder.observe(&failed, now, 1)
	current := deploymentNotificationHistoryFixture(now, "fsn-3", "disabled", time.Time{})
	recorder.observe(&current, now, 2)
	for _, event := range recorder.snapshot().Events {
		if event.Node == "fsn-2" && event.State == "unobserved" && event.Channel == "webhook" {
			if event.Reason != "not_in_current_expectations" || !event.UnresolvedObservedFailure || event.LaterSuccessAfterFailure {
				t.Fatal("retirement implied successful submission")
			}
			return
		}
	}
	t.Fatal("retirement was not disclosed")
}

func TestObsDeploymentNotificationHistoryTerminalReports(t *testing.T) {
	for _, mode := range []string{"deadline", "cancelled", "write_failed", "cancelled_before_read"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled_before_read" {
				cancel()
			}
			writes, calls := 0, 0
			options := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Second, poll: time.Second}
			report, code := runObsDeploymentVerification(ctx, options, func(context.Context) obsDeploymentSnapshot {
				calls++
				snapshot := deploymentSnapshotTest(now)
				snapshot.serving.value = deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
				snapshot.notificationErr = errors.New("private-upstream-token")
				return snapshot
			}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
				now = now.Add(d)
				if mode == "cancelled" {
					cancel()
					return ctx.Err()
				}
				return nil
			}, func(obsDeploymentReport) error {
				writes++
				if mode == "write_failed" && writes == 2 {
					return errors.New("private-report-token")
				}
				return nil
			})
			if report.Timeline == nil || report.Timeline.ProbeNotifications == nil {
				t.Fatal("terminal report lost its advisory history")
			}
			history := report.Timeline.ProbeNotifications
			if mode == "cancelled_before_read" {
				if code != 3 || calls != 0 || len(history.Events) != 0 || history.LastObservedAt != nil || history.HadSubmissionFailures {
					t.Fatal("cancellation before a read manufactured notification evidence")
				}
				return
			}
			if mode == "write_failed" && code != 2 || mode != "write_failed" && code != 3 || !history.HadSubmissionFailures || history.UnresolvedTrackedFailures != 1 || len(history.Events) < 2 || report.HadConfirmedFaults {
				t.Fatal("terminal state changed qualification or discarded notification observations")
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "private-") {
				t.Fatal("terminal report leaked provider or filesystem text")
			}
		})
	}
}
