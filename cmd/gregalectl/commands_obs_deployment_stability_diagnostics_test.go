package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func observeStabilityDiagnostics(t *testing.T, r *obsDeploymentStabilityRecorder, start, at time.Time, mutate func(*obsDeploymentSnapshot)) *obsDeploymentStability {
	t.Helper()
	s := deploymentSnapshotTest(at)
	if mutate != nil {
		mutate(&s)
	}
	checks, serving, notifications := assessObsDeployment(s, start.Add(-90*time.Second), at)
	r.observeChecks(checks, serving, notifications, at)
	return r.snapshot()
}

func TestObsDeploymentStabilityDiagnosticsExplainFrozenRun(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentStability(2 * time.Minute)
	initial := observeStabilityDiagnostics(t, r, start, start, nil)
	before, _ := json.Marshal(initial)
	latest := observeStabilityDiagnostics(t, r, start, start.Add(2*time.Minute), func(s *obsDeploymentSnapshot) {
		run := start.Add(-15 * time.Second)
		s.serving.value.Probes[0].LastRunAt = &run
	})
	d := latest.Diagnostics
	if latest.State != "stabilizing" || latest.ObservedSeconds != 45 || d.State != "available" || d.ObservationRemainingSeconds != 0 || d.Blockers.Total != 1 || len(d.Blockers.Items) != 1 || d.Blockers.Truncated {
		t.Fatalf("stalled probe was not explained: %+v %+v", latest, d)
	}
	item := d.Blockers.Items[0]
	if item.Kind != "probe_run" || item.Node != "fsn-2" || len(item.URLID) != 64 || item.Reason != "source_window_incomplete" || *item.AdvancedSeconds != 45 || *item.RemainingSeconds != 75 || !item.BaselineAt.Equal(start.Add(-time.Minute)) || !item.PreviousAt.Equal(item.BaselineAt.UTC()) || !item.LatestAt.Equal(start.Add(-15*time.Second)) {
		t.Fatalf("incorrect source clocks/progress: %+v", item)
	}
	var human bytes.Buffer
	writeObsDeploymentStability(&human, latest)
	for _, want := range []string{"stability_blockers total=1", "kind=probe_run", "node=fsn-2", "advanced_seconds=45 remaining_seconds=75"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human output omitted %q: %s", want, &human)
		}
	}
	passed := observeStabilityDiagnostics(t, r, start, start.Add(121*time.Second), nil)
	if passed.State != "passed" || passed.Diagnostics.Blockers.Total != 0 || passed.Diagnostics.Blockers.Truncated || len(passed.Diagnostics.Blockers.Items) != 0 {
		t.Fatal("recovered source retained current blockers")
	}
	after, _ := json.Marshal(initial)
	if !bytes.Equal(before, after) || d.Blockers.Total != 1 || latest.State != "stabilizing" {
		t.Fatal("later observations rewrote a saved diagnostic snapshot")
	}
}

func TestObsDeploymentStabilityDiagnosticsLocalWait(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentStability(2 * time.Second)
	observeStabilityDiagnostics(t, r, start, start, nil)
	s := deploymentSnapshotTest(start.Add(2 * time.Second))
	serving, notifications := stabilityTestProjection(t, s, start.Add(-90*time.Second), start.Add(time.Second))
	r.observe("passed", serving, notifications, start.Add(time.Second))
	d := r.snapshot().Diagnostics
	if d.State != "available" || d.ObservationRemainingSeconds != 1 || d.Blockers.Total != 0 || len(d.Blockers.Items) != 0 || r.status.Reason != "awaiting_observation_window" {
		t.Fatalf("local waiting was presented as a stale source: %+v", d)
	}
}

func TestObsDeploymentStabilityDiagnosticsAllPairsAndOrdering(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentStability(2 * time.Second)
	for poll := range 2 {
		at := start.Add(time.Duration(poll) * 2 * time.Second)
		latest := observeStabilityDiagnostics(t, r, start, at, func(s *obsDeploymentSnapshot) {
			out := &s.serving.value
			probe := out.Probes[0]
			out.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax)
			for i := range out.Probes {
				out.Probes[i] = probe
				out.Probes[i].URLID = fmt.Sprintf("%064x", i)
				if poll > 0 && i >= len(out.Probes)-6 {
					run := start.Add(-time.Minute)
					if i == len(out.Probes)-6 {
						run = run.Add(time.Second)
					}
					out.Probes[i].LastRunAt = &run
				}
			}
			expected := len(out.Probes)
			out.Inventory.ExpectedPairs = &expected
			counts := operatorobs.ServingProbeCounts(out.Probes)
			out.Counts = &counts
		})
		if latest.Diagnostics.State != "available" {
			t.Fatal("full-fleet fixture did not pass health assessment")
		}
		if poll == 0 {
			continue
		}
		preview := latest.Diagnostics.Blockers
		if preview.Total != 6 || len(preview.Items) != api.ObsPostDeployStabilityItemsMax || preview.Omitted != 2 || !preview.Truncated {
			t.Fatalf("lost slow sources outside the preview: %+v", preview)
		}
		for i, item := range preview.Items {
			if item.Kind != "probe_run" || item.URLID != fmt.Sprintf("%064x", 995+i) || *item.AdvancedSeconds != 0 {
				t.Fatalf("preview is not ordered by least progress then identity: %+v", preview.Items)
			}
		}
	}
}

func TestObsDeploymentStabilityDiagnosticsResetCauses(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		total        int
		mutate       func(*obsDeploymentSnapshot)
	}{
		{"probe policy", "probe_policy_changed", 2, func(s *obsDeploymentSnapshot) { s.serving.value.Probes[0].MaxResultAgeSeconds = 1200 }},
		{"node replacement", "source_added", 4, func(s *obsDeploymentSnapshot) { s.serving.value.Probes[0].Node = "fsn-3" }},
		{"receiver replacement", "source_added", 6, func(s *obsDeploymentSnapshot) { s.notifications.ReceiverHealth.Groups[0].Receiver = "faas-warn" }},
		{"clock regression", "source_clock_regressed", 1, func(s *obsDeploymentSnapshot) {
			at := s.notifications.GeneratedAt.Add(-2 * time.Second)
			signal := s.notifications.Signals["prometheus_queue_length"]
			signal.ObservedAt = &at
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			r := newObsDeploymentStability(2 * time.Second)
			initial := observeStabilityDiagnostics(t, r, start, start, nil)
			latest := observeStabilityDiagnostics(t, r, start, start.Add(time.Second), tc.mutate)
			reset := latest.Diagnostics.LastReset
			if reset == nil || reset.SourceContext != "reset_cause" || reset.Sources.Total != tc.total || reset.Sources.Omitted != tc.total-len(reset.Sources.Items) || !reset.At.Equal(start.Add(time.Second)) || !reset.PreviousObservedAt.Equal(start) {
				t.Fatalf("missing reset explanation: %+v", reset)
			}
			found := false
			for _, item := range reset.Sources.Items {
				if item.Reason == tc.reason {
					found = true
				}
				if item.Reason == "probe_policy_changed" && (item.MaxResultAgeSeconds != 1200 || *item.PreviousMaxResultAgeSeconds != 900 || item.BaselineAt == nil || item.PreviousAt == nil || item.LatestAt == nil) {
					t.Fatal("policy reset lost before/after evidence")
				}
				if item.Reason == "source_clock_regressed" && (item.Name != "prometheus_queue_length" || !item.LatestAt.Before(*item.PreviousAt)) {
					t.Fatal("clock reset lost its source or original timestamps")
				}
				if item.Reason == "source_added" && (item.BaselineAt != nil || item.PreviousAt != nil || item.LatestAt == nil) || item.Reason == "source_removed" && (item.LatestAt != nil || item.PreviousAt == nil || item.BaselineAt == nil) {
					t.Fatal("identity changes invented source clocks")
				}
			}
			if !found || initial.Diagnostics.LastReset != nil {
				t.Fatal("reset cause was omitted incorrectly or changed an older snapshot")
			}
			captured, _ := json.Marshal(reset)
			// The next healthy window retains the latest reset as history.
			if tc.name == "clock regression" {
				tc.mutate = nil
			}
			passed := observeStabilityDiagnostics(t, r, start, start.Add(3*time.Second), tc.mutate)
			retained, _ := json.Marshal(passed.Diagnostics.LastReset)
			if passed.State != "passed" || !bytes.Equal(captured, retained) {
				t.Fatal("later recovery discarded or rewrote the reset cause")
			}
		})
	}
}

func TestObsDeploymentStabilityDiagnosticsLossPreservesPreviousBlockers(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentStability(2 * time.Second)
	initial := r.snapshot()
	if initial.Diagnostics.State != "not_observed" || initial.Diagnostics.Blockers != nil {
		t.Fatal("initial report invented assessed sources")
	}
	observeStabilityDiagnostics(t, r, start, start, nil)
	previousWindow := observeStabilityDiagnostics(t, r, start, start.Add(time.Second), func(s *obsDeploymentSnapshot) {
		old := start.Add(-time.Minute)
		s.serving.value.Probes[0].LastRunAt = &old
	})
	latest := observeStabilityDiagnostics(t, r, start, start.Add(2*time.Second), func(s *obsDeploymentSnapshot) { s.notificationErr = errors.New("https://private-provider/token") })
	d := latest.Diagnostics
	if d.State != "unavailable" || d.Blockers != nil || len(d.Checks) != 2 || d.LastReset.SourceContext != "previous_window_blockers" || d.LastReset.Sources.Total != 18 || len(d.LastReset.Checks) != 2 || !d.LastReset.PreviousObservedAt.Equal(start.Add(time.Second)) {
		t.Fatalf("source loss hid earlier blockers or presented old data as current: %+v", d)
	}
	if !reflect.DeepEqual(previousWindow.Diagnostics.Blockers, d.LastReset.Sources) {
		t.Fatal("source loss did not retain the exact preceding blocker preview")
	}
	previous := d.LastReset.Sources.Items
	// The unchanged run is the least-progressing source in the old window.
	if previous[0].Kind != "probe_run" || *previous[0].AdvancedSeconds != 0 || !previous[0].LatestAt.Equal(start.Add(-time.Minute)) {
		t.Fatal("previous window evidence was re-stamped on source loss")
	}
	body, err := json.Marshal(latest)
	if err != nil || strings.Contains(string(body), "private-provider") || strings.Contains(string(body), "token") {
		t.Fatal("provider error text entered source diagnostics")
	}
	for _, elapsed := range []time.Duration{3 * time.Second, 4 * time.Second} {
		next := observeStabilityDiagnostics(t, r, start, start.Add(elapsed), func(s *obsDeploymentSnapshot) { s.serving.value.Probes[0].Node = "https://private-node/token" })
		if next.Resets != 1 || !reflect.DeepEqual(next.Diagnostics.LastReset, d.LastReset) {
			t.Fatal("repeated unknown evidence replaced the last actual reset")
		}
	}
}

func TestObsDeploymentStabilityDiagnosticsTerminalRetention(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "write_failure"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			now := start
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second, stableFor: 2 * time.Second}
			writes, waits := 0, 0
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				s := deploymentSnapshotTest(now)
				old := start.Add(-time.Minute)
				s.serving.value.Probes[0].LastRunAt = &old
				return s
			}, func() time.Time { return now }, func(context.Context, time.Duration) error {
				waits++
				if mode == "cancel" && waits == 2 {
					cancel()
					return ctx.Err()
				}
				now = now.Add(time.Second)
				return nil
			}, func(obsDeploymentReport) error {
				writes++
				if mode == "write_failure" && writes == 3 {
					return errors.New("private-write-error")
				}
				return nil
			})
			want := 3
			if mode == "write_failure" {
				want = 2
			}
			preview := report.Stability.Diagnostics.Blockers
			if code != want || preview == nil || preview.Total == 0 || preview.Items[0].Kind != "probe_run" || *preview.Items[0].AdvancedSeconds != 0 || report.Stability.LastObservedAt == nil {
				t.Fatalf("terminal result lost source details: code=%d stability=%+v", code, report.Stability)
			}
			if mode == "timeout" && (preview.Total != 1 || report.Stability.State != "incomplete") {
				t.Fatal("timeout lost the source that held back qualification")
			}
		})
	}
}

func TestObsDeploymentStabilityDiagnosticsPreviewBounds(t *testing.T) {
	// Exercise the byte cap independently of the validated source-field limits.
	items := make([]obsDeploymentStabilityDetail, api.ObsPostDeployStabilityItemsMax+1)
	for i := range items {
		items[i] = obsDeploymentStabilityDetail{Kind: "probe_run", Node: strings.Repeat("n", api.ObsPostDeployStabilityBytesMax/2), Reason: "source_window_incomplete"}
	}
	preview := retainObsDeploymentStabilityPreview(items)
	if len(preview.Items) == 0 || len(preview.Items) >= api.ObsPostDeployStabilityItemsMax || preview.Omitted != len(items)-len(preview.Items) || !preview.Truncated || preview.RetainedBytes > preview.ByteLimit {
		t.Fatalf("byte bound or truncation counts failed: %+v", preview)
	}
	body, err := json.MarshalIndent(preview.Items, "          ", "  ")
	if err != nil || len(body) > preview.RetainedBytes || preview.RetainedBytes != obsDeploymentStabilityPreviewBytes(preview.Items) {
		t.Fatal("preview byte accounting undercharged pretty JSON")
	}
	preview = retainObsDeploymentStabilityPreview(append(items, items...))
	if preview.Total != 2*len(items) || preview.RetainedBytes > api.ObsPostDeployStabilityBytesMax {
		t.Fatal("source count changed the serialized byte ceiling")
	}
}

func TestObsDeploymentStabilityDiagnosticsObservationResets(t *testing.T) {
	for _, tc := range []struct {
		offset time.Duration
		reason string
	}{
		{-time.Second, "observation_clock_regressed"},
		{api.ObsHealthFreshnessMaxAge + time.Second, "observation_gap"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			r := newObsDeploymentStability(2 * time.Second)
			observeStabilityDiagnostics(t, r, start, start, nil)
			latest := observeStabilityDiagnostics(t, r, start, start.Add(tc.offset), nil)
			reset := latest.Diagnostics.LastReset
			if reset == nil || reset.Reason != tc.reason || !reset.PreviousObservedAt.Equal(start) || !reset.At.Equal(start.Add(tc.offset)) || reset.SourceContext != "previous_window_blockers" || reset.Sources.Total != 18 {
				t.Fatalf("observation reset lost its clocks and prior evidence: %+v", reset)
			}
			if tc.reason == "observation_gap" && reset.ObservationGapSeconds != tc.offset.Seconds() {
				t.Fatal("observation gap was not disclosed")
			}
		})
	}
}
