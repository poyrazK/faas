package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObsDeploymentTimelineRecoveryPreservesFaultAndSnapshots(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	now := started
	calls := 0
	var saved []obsDeploymentReport
	fetch := func(context.Context) obsDeploymentSnapshot {
		calls++
		snapshot := deploymentSnapshotTest(now)
		switch calls {
		case 1, 2:
			deploymentProbeTestState(&snapshot, "missing", "result_missing")
		case 3, 4:
			snapshot.serving.err = errors.New("private-provider-error")
		}
		return snapshot
	}
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: started.Add(-90 * time.Second), wait: 10 * time.Second, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, fetch, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
		now = now.Add(d)
		return nil
	}, func(report obsDeploymentReport) error { saved = append(saved, report); return nil })
	if code != 0 || report.State != "passed" || !report.HadConfirmedFaults || report.Attempts != 5 {
		t.Fatalf("unexpected final report: %+v, exit=%d", report, code)
	}
	history := report.Timeline
	if len(history.Transitions) != 5 || history.Truncated || history.DroppedTransitions != 0 {
		t.Fatalf("duplicate polls became transitions: %+v", history)
	}
	wantStates, wantAttempts := []string{"degraded", "unknown", "passed"}, []int{1, 3, 5}
	var serving []obsDeploymentTransition
	for _, transition := range history.Transitions {
		if transition.Check == "serving_probes" {
			serving = append(serving, transition)
		}
	}
	if len(serving) != len(wantStates) {
		t.Fatalf("serving timeline = %+v", serving)
	}
	for i, transition := range serving {
		if transition.State != wantStates[i] || transition.Attempt != wantAttempts[i] || !transition.At.Equal(started.Add(time.Duration(wantAttempts[i]-1)*time.Second)) {
			t.Fatalf("transition %d = %+v", i, transition)
		}
	}
	fault := history.Checks[0]
	if fault.FirstFaultAt == nil || !fault.FirstFaultAt.Equal(started) || fault.FirstFaultReason != "serving_probe_fault" || fault.LastRecoveryAt == nil || !fault.LastRecoveryAt.Equal(now) || fault.ObservedFaultSeconds != 2 || fault.UnrecoveredFault {
		t.Fatalf("fault summary lost cause or counted unknown time: %+v", fault)
	}
	for _, healthy := range history.Checks[1:] {
		if healthy.FirstFaultAt != nil || healthy.LastRecoveryAt != nil || healthy.ObservedFaultSeconds != 0 || healthy.UnrecoveredFault {
			t.Fatalf("healthy check gained fault history: %+v", healthy)
		}
	}
	if len(saved[0].Timeline.Transitions) != 0 || saved[0].Timeline.Checks[0].FirstFaultAt != nil || saved[0].Timeline.Checks[0].LastObservedAt != nil {
		t.Fatal("initial unknown report acquired later observations")
	}
	if saved[1].Timeline.Checks[0].LastRecoveryAt != nil || len(saved[1].Timeline.Transitions) != 3 || saved[1].Timeline.Checks[0].ObservedFaultSeconds != 0 {
		t.Fatal("later polls mutated the first fault snapshot")
	}
	body, err := json.Marshal(report)
	if err != nil || strings.Contains(string(body), "private-provider-error") {
		t.Fatalf("history leaked provider diagnostics: %s, %v", body, err)
	}
}

func TestObsDeploymentTimelineDeadlineAndCancellation(t *testing.T) {
	for _, mode := range []string{"degraded", "unknown", "cancelled", "write_failed", "cancelled_before_poll"} {
		t.Run(mode, func(t *testing.T) {
			started := time.Now().UTC().Truncate(time.Second)
			now, calls, writes := started, 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled_before_poll" {
				cancel()
			}
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: started.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			fetch := func(context.Context) obsDeploymentSnapshot {
				calls++
				snapshot := deploymentSnapshotTest(now)
				deploymentProbeTestState(&snapshot, "missing", "result_missing")
				if mode == "unknown" && calls > 1 {
					snapshot.serving.err = errors.New("private-provider-error")
				}
				return snapshot
			}
			report, code := runObsDeploymentVerification(ctx, opts, fetch, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
				now = now.Add(d)
				if mode == "cancelled" {
					cancel()
					return ctx.Err()
				}
				return nil
			}, func(obsDeploymentReport) error {
				writes++
				if mode == "write_failed" && writes == 2 {
					return errors.New("private-file-error")
				}
				return nil
			})
			if report.Timeline == nil {
				t.Fatal("terminal report lost its timeline")
			}
			if mode == "cancelled_before_poll" {
				if code != 3 || calls != 0 || len(report.Timeline.Transitions) != 0 {
					t.Fatal("cancellation fabricated a health observation")
				}
				return
			}
			fault := report.Timeline.Checks[0]
			if fault.FirstFaultAt == nil || !fault.FirstFaultAt.Equal(started) || fault.LastRecoveryAt != nil || !fault.UnrecoveredFault || !report.HadConfirmedFaults {
				t.Fatalf("terminal report lost an unresolved fault: %+v", report)
			}
			wantCode, wantSeconds, wantReason := 3, 0.0, "cancelled"
			switch mode {
			case "degraded":
				wantCode, wantSeconds, wantReason = 1, 2, "confirmed_monitoring_fault"
			case "unknown":
				wantSeconds, wantReason = 1, "incomplete_monitoring_evidence"
			case "write_failed":
				wantCode, wantReason = 2, "report_write_failed"
			}
			if code != wantCode || report.Reason != wantReason || fault.ObservedFaultSeconds != wantSeconds {
				t.Fatalf("exit=%d reason=%s summary=%+v", code, report.Reason, fault)
			}
			if mode == "cancelled" && !fault.LastObservedAt.Equal(started) {
				t.Fatal("cancellation advanced the last health observation")
			}
		})
	}
}

func TestObsDeploymentTimelineReasonChangesAndRepeatedRecoveries(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	check := obsDeploymentCheckFor("notification_pipeline")
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	observations := []struct {
		second int
		state  string
		reason string
	}{
		{0, "degraded", "notification_pipeline_fault"},
		{1, "degraded", "notification_queue_backlog"},
		{2, "unknown", "source_unavailable"},
		{5, "passed", ""},
		{6, "passed", ""},
		{10, "degraded", "notification_integration_fault"},
		{12, "passed", ""},
		{14, "passed", ""},
	}
	var history *obsDeploymentTimeline
	for i, observation := range observations {
		check.State, check.Reason = observation.state, observation.reason
		history = recorder.observe([]obsDeploymentCheck{check}, started.Add(time.Duration(observation.second)*time.Second), i+1)
	}
	fault := history.Checks[0]
	if len(history.Transitions) != 6 || history.Transitions[1].Reason != "notification_queue_backlog" {
		t.Fatalf("reason changes or duplicate polls were lost: %+v", history.Transitions)
	}
	if fault.FirstFaultReason != "notification_pipeline_fault" || !fault.FirstFaultAt.Equal(started) || !fault.LastRecoveryAt.Equal(started.Add(12*time.Second)) || fault.ObservedFaultSeconds != 4 || fault.UnrecoveredFault {
		t.Fatalf("repeated episodes or unknown gaps were miscounted: %+v", fault)
	}
}

func TestObsDeploymentTimelineTruncationPreservesSummariesAndEarlierSnapshots(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	check := obsDeploymentCheckFor("notification_pipeline")
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	var first, history *obsDeploymentTimeline
	count := api.ObsPostDeployTransitionsMax + 7
	for attempt := 1; attempt <= count; attempt++ {
		check.State, check.Reason = "degraded", "notification_pipeline_fault"
		if attempt%2 == 0 {
			check.Reason = "notification_queue_backlog"
		}
		history = recorder.observe([]obsDeploymentCheck{check}, started.Add(time.Duration(attempt-1)*time.Second), attempt)
		if attempt == 1 {
			first = history
		}
	}
	check.State, check.Reason = "passed", ""
	history = recorder.observe([]obsDeploymentCheck{check}, started.Add(time.Duration(count)*time.Second), count+1)
	if len(history.Transitions) != api.ObsPostDeployTransitionsMax || history.TransitionLimit != api.ObsPostDeployTransitionsMax || history.DroppedTransitions != 8 || !history.Truncated {
		t.Fatalf("unbounded or unmarked history: %+v", history)
	}
	if history.Transitions[0].Attempt != 9 || history.Transitions[len(history.Transitions)-1].Attempt != count+1 {
		t.Fatal("history did not retain the latest transitions")
	}
	fault := history.Checks[0]
	if !fault.FirstFaultAt.Equal(started) || fault.FirstFaultReason != "notification_pipeline_fault" || fault.ObservedFaultSeconds != float64(count) || fault.LastRecoveryAt == nil || fault.UnrecoveredFault {
		t.Fatalf("truncation discarded the first cause or recovery: %+v", fault)
	}
	if first.Truncated || first.Transitions[0].Attempt != 1 || first.Checks[0].LastRecoveryAt != nil || first.Checks[0].ObservedFaultSeconds != 0 {
		t.Fatal("buffer rollover mutated an earlier report")
	}
	body, err := json.MarshalIndent(history, "", "  ")
	if err != nil || len(body) > 128<<10 {
		t.Fatalf("bounded metadata consumed too much of the report envelope: %d bytes, %v", len(body), err)
	}
}

func TestObsDeploymentTimelineHumanSummaryAndUTC(t *testing.T) {
	started := time.Date(2026, 11, 13, 15, 0, 0, 0, time.FixedZone("fixture", 3600))
	check := obsDeploymentCheckFor("serving_probes")
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	check.State, check.Reason = "degraded", "serving_probe_fault"
	history := recorder.observe([]obsDeploymentCheck{check}, started, 1)
	var out strings.Builder
	writeObsDeploymentHuman(&out, obsDeploymentReport{State: "unknown", HadConfirmedFaults: true, Timeline: history})
	if !strings.Contains(out.String(), "last_recovery_at=unknown") || !strings.Contains(out.String(), "unrecovered_fault=true") {
		t.Fatal("unresolved fault was rendered as recovered")
	}
	check.State, check.Reason = "passed", ""
	history = recorder.observe([]obsDeploymentCheck{check}, started.Add(3*time.Second), 2)
	out.Reset()
	writeObsDeploymentHuman(&out, obsDeploymentReport{State: "passed", HadConfirmedFaults: true, Timeline: history})
	for _, want := range []string{"had_confirmed_faults=true", "transitions=2", "first_fault_at=2026-11-13T14:00:00Z", "last_recovery_at=2026-11-13T14:00:03Z", "observed_fault_seconds=3", "unrecovered_fault=false"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human summary missing %q: %s", want, &out)
		}
	}
}
