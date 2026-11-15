package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestObsDeploymentStabilityQualification(t *testing.T) {
	for _, tc := range []struct {
		name, mode, state, reason, reset string
		code, attempts                   int
		had                              bool
	}{
		{"advancing evidence", "", "passed", "", "", 0, 3, false},
		{"same evidence with fresh response headers", "frozen", "unknown", "stability_window_incomplete", "", 3, 6, false},
		{"new evaluations of the same probe run", "frozen_run", "unknown", "stability_window_incomplete", "", 3, 6, false},
		{"new success values with frozen observations", "frozen_success", "unknown", "stability_window_incomplete", "", 3, 6, false},
		{"degradation restarts the window", "degraded", "passed", "", "checks_degraded", 0, 6, true},
		{"unknown restarts the window", "unknown", "passed", "", "checks_unknown", 0, 6, false},
		{"degradation at the deadline", "fault", "degraded", "confirmed_monitoring_fault", "checks_degraded", 1, 6, true},
		{"unknown at the deadline", "unavailable", "unknown", "incomplete_monitoring_evidence", "checks_unknown", 3, 6, false},
		{"direct notification failure stays advisory", "advisory", "passed", "", "", 0, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			now := start
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: start.Add(-90 * time.Second), wait: 5 * time.Second, poll: time.Second, stableFor: 2 * time.Second}
			var saved []obsDeploymentReport
			calls := 0
			fetch := func(context.Context) obsDeploymentSnapshot {
				calls++
				s := deploymentSnapshotTest(now)
				switch tc.mode {
				case "frozen":
					s = deploymentSnapshotTest(start)
					s.serving.value.GeneratedAt, s.notifications.GeneratedAt = now, now
				case "frozen_run":
					run := start.Add(-time.Minute)
					s.serving.value.Probes[0].LastRunAt = &run
				case "frozen_success":
					s.notifications.ReceiverHealth.Groups[0].LastSuccessObservation.ObservedAt = &start
				case "degraded", "fault":
					if calls == 2 || calls == 3 || tc.mode == "fault" && calls > 1 {
						deploymentProbeTestState(&s, "missing", "result_missing")
					}
				case "unknown", "unavailable":
					if calls == 2 || calls == 3 || tc.mode == "unavailable" && calls > 1 {
						s.notificationErr = errors.New("private-provider-detail")
					}
				case "advisory":
					s.serving.value = deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", start.Add(-24*time.Hour))
				}
				return s
			}
			report, code := runObsDeploymentVerification(context.Background(), opts, fetch, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(r obsDeploymentReport) error { saved = append(saved, r); return nil })
			if code != tc.code || report.State != tc.state || report.Reason != tc.reason || calls != tc.attempts || report.HadConfirmedFaults != tc.had {
				t.Fatalf("code=%d calls=%d report=%+v", code, calls, report)
			}
			stability := report.Stability
			if stability == nil || stability.RequiredSeconds != 2 || stability.LastResetReason != tc.reset || len(saved) != calls+1 {
				t.Fatalf("lost progress or reset: stability=%+v saves=%d", stability, len(saved))
			}
			if tc.reset != "" && (stability.Resets != 1 || stability.LastResetAt == nil || !stability.LastResetAt.Equal(start.Add(time.Second))) {
				t.Fatal("repeated non-passing checks counted extra resets")
			}
			if tc.reset != "" && (stability.Diagnostics.LastReset == nil || len(stability.Diagnostics.LastReset.Checks) == 0 || stability.Diagnostics.LastReset.Checks[0].Reason == "") {
				t.Fatal("health reset omitted its reviewed check reason")
			}
			if tc.code == 0 {
				if stability.State != "passed" || stability.ObservedSeconds != 2 || stability.SourceSpanSeconds != 2 || stability.SourceCount != 18 {
					t.Fatalf("incorrect advancing evidence summary: %+v", stability)
				}
			} else if stability.State != "incomplete" || stability.ObservedSeconds != 0 {
				t.Fatalf("incomplete evidence qualified: %+v", stability)
			}
			if saved[0].Stability.State != "waiting" || saved[0].Stability.LastObservedAt != nil || saved[1].State != "unknown" || saved[1].Reason != "awaiting_stability" || saved[1].Stability.ObservedSeconds != 0 {
				t.Fatal("later polling mutated an earlier snapshot or first pass ended the window")
			}
			for _, check := range saved[1].Checks {
				if check.State != "passed" {
					t.Fatal("stability hold rewrote the original health checks")
				}
			}
			if tc.mode == "advisory" && (!report.Timeline.ProbeNotifications.HadSubmissionFailures || report.Timeline.ProbeNotifications.UnresolvedTrackedFailures != 1) {
				t.Fatal("stability qualification discarded advisory failures")
			}
			body, err := json.Marshal(report)
			if err != nil || strings.Contains(string(body), "private-provider-detail") {
				t.Fatal("unsafe stability report")
			}
			var human bytes.Buffer
			writeObsDeploymentHuman(&human, report)
			if !strings.Contains(human.String(), "stability state="+stability.State) || !strings.Contains(human.String(), "observed_seconds=") {
				t.Fatal("human output omitted progress")
			}
		})
	}
}

func TestObsDeploymentStabilityTerminalPaths(t *testing.T) {
	for _, mode := range []string{"cancel_before_read", "cancel_during_read", "cancel_wait", "write_initial", "write_poll", "write_crosses_deadline", "write_terminal"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			start := now
			opts := obsDeploymentOptions{after: now.Add(-90 * time.Second), wait: 5 * time.Second, poll: time.Second, stableFor: 2 * time.Second}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel_before_read" {
				cancel()
			}
			calls, writes := 0, 0
			var saved obsDeploymentReport
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				if mode == "cancel_during_read" {
					cancel()
				}
				return deploymentSnapshotTest(now)
			}, func() time.Time { return now }, func(context.Context, time.Duration) error { cancel(); return ctx.Err() }, func(r obsDeploymentReport) error {
				writes++
				saved = r
				if mode == "write_initial" || mode == "write_poll" && writes == 2 || mode == "write_terminal" && writes == 3 {
					return errors.New("private-write-error")
				}
				if (mode == "write_crosses_deadline" || mode == "write_terminal") && writes == 2 {
					now = start.Add(opts.wait)
				}
				return nil
			})
			wantCode, wantState, wantReason := 3, "interrupted", "cancelled"
			if strings.HasPrefix(mode, "write_") {
				wantCode, wantReason = 2, "report_write_failed"
			}
			if mode == "write_crosses_deadline" {
				wantCode, wantState, wantReason = 3, "incomplete", "stability_window_incomplete"
				if calls != 1 || writes != 3 || !saved.UpdatedAt.Equal(now) || saved.Stability.State != "incomplete" {
					t.Fatal("write deadline did not persist a terminal summary without an extra read")
				}
			}
			if code != wantCode || report.Reason != wantReason || report.Stability.State != wantState || report.Stability.ObservedSeconds != 0 {
				t.Fatalf("code=%d report=%+v stability=%+v", code, report, report.Stability)
			}
			if mode == "cancel_before_read" || mode == "write_initial" {
				if calls != 0 || report.Stability.LastObservedAt != nil {
					t.Fatal("terminal result invented an observation")
				}
			} else if calls != 1 || !report.Stability.LastObservedAt.Equal(start) {
				t.Fatal("terminal result changed the last observation")
			}
		})
	}
}

func TestObsDeploymentStabilityWaitBudget(t *testing.T) {
	for _, mode := range []string{"policy", "late_read"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			start := now
			opts := obsDeploymentOptions{after: now.Add(-90 * time.Second), poll: time.Minute, stableFor: 30 * time.Minute}
			if mode == "late_read" {
				opts.wait, opts.poll, opts.stableFor = 2*time.Second, time.Second, 2*time.Second
			}
			calls := 0
			report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				if mode == "late_read" && calls == 2 {
					now = start.Add(opts.wait + time.Nanosecond)
				}
				return deploymentSnapshotTest(now)
			}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(obsDeploymentReport) error { return nil })
			if code != 3 || report.Reason != "stability_window_incomplete" || report.Stability.State != "incomplete" || report.Stability.Resets != 0 {
				t.Fatalf("stability escaped its wait budget: code=%d report=%+v", code, report)
			}
			if mode == "policy" && (report.WaitSeconds != (17*time.Minute).Seconds() || !now.Equal(start.Add(17*time.Minute)) || calls != 18) {
				t.Fatal("stability extended the policy-derived wait")
			}
			if mode == "late_read" && (calls != 2 || report.Stability.Reason != "observed_after_deadline") {
				t.Fatal("late evidence lost its deadline explanation")
			}
		})
	}
}
