package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObsDeploymentSourcesRecoveryAndStatusHistory(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	now, calls := start, 0
	var saved []obsDeploymentReport
	opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: 10 * time.Second, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot {
		calls++
		s := deploymentSnapshotTest(now)
		if calls <= 4 {
			status := 403
			if calls == 3 {
				status = 503
			}
			if calls == 4 {
				status = 502
			}
			s.notificationErr = obsFetchHTTPFailure("notification-health", status)
		}
		s.notificationTiming = obsDeploymentReadTime(now.Add(-500*time.Millisecond), now.Add(-100*time.Millisecond))
		s.serving.timing = obsDeploymentReadTime(now.Add(-500*time.Millisecond), now.Add(-200*time.Millisecond))
		return s
	}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(r obsDeploymentReport) error { saved = append(saved, r); return nil })
	if code != 0 || report.HadConfirmedFaults || calls != 5 || report.State != "passed" {
		t.Fatalf("read failures changed health qualification: code=%d report=%+v", code, report)
	}
	if saved[0].Sources.NotificationHealth.State != "not_read" || saved[0].Sources.NotificationHealth.LastReadAt != nil || saved[0].Sources.ServingHealth.Reads != 0 {
		t.Fatal("initial report invented source reads")
	}
	source := report.Sources.NotificationHealth
	if source.State != "succeeded" || source.Reads != 5 || source.FailedReads != 4 || source.CancelledReads != 0 || source.ConsecutiveFailures != 0 || *source.HTTPStatus != 200 || source.LastFailure.Reason != "server_error" || *source.LastFailure.HTTPStatus != 502 || *source.LastReadSeconds != .4 {
		t.Fatalf("read counters or last failure were lost: %+v", source)
	}
	if !source.FirstFailureAt.Equal(start.Add(-100*time.Millisecond)) || !source.LastRecoveryAt.Equal(now.Add(-100*time.Millisecond)) || !source.LastSuccessAt.Equal(*source.LastReadCompletedAt) || !source.LastFailure.At.Equal(start.Add(2900*time.Millisecond)) {
		t.Fatal("read summary did not retain per-request completion clocks")
	}
	if old := saved[1].Sources.NotificationHealth; old.Reads != 1 || old.FailedReads != 1 || old.LastRecoveryAt != nil || *old.HTTPStatus != 403 || old.LastFailure.Reason != "forbidden" {
		t.Fatal("later polls rewrote an earlier source summary")
	}
	var transitions []obsDeploymentTransition
	for _, transition := range report.Timeline.Transitions {
		if transition.Check == "notification_pipeline" {
			transitions = append(transitions, transition)
		}
	}
	if len(transitions) != 4 || transitions[0].Attempt != 1 || transitions[1].Attempt != 3 || transitions[2].Attempt != 4 || transitions[3].State != "passed" || *transitions[0].SourceHTTPStatus != 403 || *transitions[1].SourceHTTPStatus != 503 || *transitions[2].SourceHTTPStatus != 502 {
		t.Fatalf("repeated status or changed HTTP class was misrecorded: %+v", transitions)
	}
	if report.Sources.ServingHealth.Reads != 5 || report.Sources.ServingHealth.FailedReads != 0 || report.Sources.ServingHealth.LastRecoveryAt != nil {
		t.Fatal("shared notification source counted failures against serving")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, report)
	for _, want := range []string{"source=notification_health state=succeeded", "failed_reads=4", "reason=server_error http_status=502", "reason=source_forbidden http_status=403", "duration_seconds=0.4"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human output missing %q", want)
		}
	}
}

func TestObsDeploymentSourceStatusHistoryOwnsInput(t *testing.T) {
	now := time.Now().UTC()
	status := 503
	check := obsDeploymentCheckFor("notification_pipeline")
	check.Reason, check.SourceHTTPStatus = "source_server_error", &status
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	first := recorder.observe([]obsDeploymentCheck{check}, now, 1)
	status = 502
	next := recorder.observe([]obsDeploymentCheck{check}, now.Add(time.Second), 2)
	if len(next.Transitions) != 2 || *first.Transitions[0].SourceHTTPStatus != 503 || *next.Transitions[1].SourceHTTPStatus != 502 {
		t.Fatal("mutable source metadata hid a status change or rewrote earlier history")
	}
}

func TestObsDeploymentSourcesCancellationAndReadableStaleData(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	recorder := &obsDeploymentSourceRecorder{}
	s := deploymentSnapshotTest(start)
	s.notificationErr = obsFetchHTTPFailure("notification-health", 403)
	first := recorder.observe(s, true, false, start)
	s.notificationErr = context.Canceled
	cancelled := recorder.observe(s, true, false, start.Add(time.Second))
	if cancelled.NotificationHealth.State != "cancelled" || cancelled.NotificationHealth.FailedReads != 1 || cancelled.NotificationHealth.CancelledReads != 1 || cancelled.NotificationHealth.ConsecutiveFailures != 1 || cancelled.NotificationHealth.LastRecoveryAt != nil || cancelled.NotificationHealth.LastFailure.Reason != "forbidden" {
		t.Fatal("cancellation became an outage or manufactured recovery")
	}
	// Successful collection can recover while health evidence remains stale.
	s = deploymentSnapshotTest(start.Add(-3 * time.Minute))
	checks, serving, notifications := assessObsDeployment(s, start.Add(-90*time.Second), start.Add(2*time.Second))
	readable := recorder.observe(s, serving != nil, notifications != nil, start.Add(2*time.Second))
	if state, _ := obsDeploymentOverall(checks); state != "unknown" || readable.NotificationHealth.State != "succeeded" || readable.NotificationHealth.ConsecutiveFailures != 0 || readable.NotificationHealth.LastRecoveryAt == nil {
		t.Fatal("read recovery was confused with healthy monitoring evidence")
	}
	if first.NotificationHealth.Reads != 1 || first.NotificationHealth.CancelledReads != 0 || first.NotificationHealth.LastRecoveryAt != nil {
		t.Fatal("source history did not own previous snapshots")
	}
}

func TestObsDeploymentSourcesInvalidAndUntypedFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"serving contract", "notification contract", "untyped error", "unknown typed reason"} {
		t.Run(mode, func(t *testing.T) {
			s := deploymentSnapshotTest(now)
			switch mode {
			case "serving contract":
				s.serving.value.Scope = "private-invalid-contract"
			case "notification contract":
				s.notifications.GeneratedAt = time.Time{}
			case "untyped error":
				s.notificationErr = errors.New("HTTP 403: private-provider-url")
			case "unknown typed reason":
				s.notificationErr = &obsFetchFailure{reason: "private-unsafe-reason", message: "private-error"}
			}
			checks, serving, notifications := assessObsDeployment(s, now.Add(-90*time.Second), now)
			if state, _ := obsDeploymentOverall(checks); state != "unknown" {
				t.Fatal("unavailable read became a confirmed fault")
			}
			r := &obsDeploymentSourceRecorder{}
			sources := r.observe(s, serving != nil, notifications != nil, now)
			source := sources.NotificationHealth
			if mode == "serving contract" {
				source = sources.ServingHealth
			}
			want := "invalid_response"
			if mode == "untyped error" || mode == "unknown typed reason" {
				want = "unavailable"
			}
			if source.State != "failed" || source.Reason != want || source.HTTPStatus != nil || source.LastSuccessAt != nil {
				t.Fatalf("incorrect invalid response diagnostics: %+v", source)
			}
			body, err := json.Marshal([]any{sources, checks})
			if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "403") {
				t.Fatal("unreviewed text entered source diagnostics or was parsed as status")
			}
		})
	}
}

func TestObsDeploymentSourcesSurviveTimelineRollover(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	now, calls := start, 0
	opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: 10 * time.Minute, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot {
		calls++
		s := deploymentSnapshotTest(now)
		if calls <= api.ObsPostDeployTransitionsMax+1 {
			status := 503
			if calls%2 == 0 {
				status = 502
			}
			s.notificationErr = obsFetchHTTPFailure("notification-health", status)
		}
		return s
	}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(obsDeploymentReport) error { return nil })
	if code != 0 || report.HadConfirmedFaults || !report.Timeline.Truncated || len(report.Timeline.Transitions) != api.ObsPostDeployTransitionsMax || report.Sources.NotificationHealth.FailedReads != api.ObsPostDeployTransitionsMax+1 || report.Sources.NotificationHealth.Reads != calls || report.Sources.NotificationHealth.LastRecoveryAt == nil {
		t.Fatalf("timeline rollover lost cumulative source history: code=%d sources=%+v", code, report.Sources)
	}
	for _, check := range report.Timeline.Checks {
		if check.FirstFaultAt != nil || check.UnrecoveredFault {
			t.Fatal("source failures manufactured confirmed health faults")
		}
	}
}

func TestObsDeploymentSourcesTerminalPaths(t *testing.T) {
	for _, mode := range []string{"cancel before read", "cancel after read", "timeout", "initial write error", "observed write error"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel before read" {
				cancel()
			}
			opts := obsDeploymentOptions{after: now.Add(-90 * time.Second), wait: time.Second, poll: time.Second}
			calls, writes := 0, 0
			var observed *obsDeploymentSources
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				s := deploymentSnapshotTest(now)
				s.notificationErr = obsFetchHTTPFailure("notification-health", 403)
				return s
			}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
				if mode == "cancel after read" {
					cancel()
					return ctx.Err()
				}
				now = now.Add(d)
				return nil
			}, func(r obsDeploymentReport) error {
				writes++
				if r.Attempts > 0 {
					observed = r.Sources
				}
				if mode == "initial write error" || mode == "observed write error" && writes == 2 {
					return errors.New("private-write-path")
				}
				return nil
			})
			want := 3
			if strings.Contains(mode, "write error") {
				want = 2
			}
			if code != want || report.HadConfirmedFaults {
				t.Fatalf("code=%d report=%+v", code, report)
			}
			if calls == 0 {
				if report.Sources.NotificationHealth.State != "not_read" || report.Sources.NotificationHealth.Reads != 0 {
					t.Fatal("terminal handling invented source reads")
				}
			} else if !reflect.DeepEqual(report.Sources, observed) || report.Sources.NotificationHealth.Reads != calls || report.Sources.NotificationHealth.LastFailure.Reason != "forbidden" {
				t.Fatal("terminal handling replaced the last observed source diagnostics")
			}
		})
	}
}

func maximumDeploymentSources(now time.Time) *obsDeploymentSources {
	status := 200
	at := now.Add(-time.Nanosecond)
	first := now.Add(-api.ObsPostDeployWaitMax)
	started := now.Add(-api.ObsPostDeployHTTPTimeout)
	seconds := 9.123456789
	count := int(api.ObsPostDeployWaitMax.Seconds())
	summary := obsDeploymentSourceSummary{State: "succeeded", HTTPStatus: &status, Reads: count, FailedReads: count - 2, CancelledReads: 1,
		LastReadAt: &now, LastReadStartedAt: &started, LastReadCompletedAt: &now, LastReadSeconds: &seconds,
		LastSuccessAt: &now, FirstFailureAt: &first, LastRecoveryAt: &now,
		LastFailure: &obsDeploymentSourceFailure{At: at, Reason: "response_read_failed", HTTPStatus: &status}}
	return &obsDeploymentSources{ServingHealth: summary, NotificationHealth: summary}
}
