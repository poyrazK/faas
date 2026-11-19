package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func buildHistoryCurrent(t *testing.T, now time.Time, state string) *obsDeploymentRuntimeBuilds {
	t.Helper()
	if state == "unknown" {
		return &obsDeploymentRuntimeBuilds{Advisory: true, State: state, Reason: "source_server_error", SourceHTTPStatus: obsDeploymentEvidencePtr(newInt(503))}
	}
	e := deploymentRuntimeBuildTest(t, now, now.Add(-time.Minute), strings.Repeat("a", 40))
	if state == "mixed" {
		e.State, e.Reason = state, "multiple_observed_revisions"
		e.Counts.Matching, e.Counts.Mismatched, e.Counts.DistinctRevisions = 3, 1, 2
		e.Items[0].State, e.Items[0].Reason, e.Items[0].GitSHA = "mismatched", "requested_revision_mismatch", strings.Repeat("b", 40)
	}
	if state == "incomplete" {
		e.State, e.Reason = state, "incomplete_identity_evidence"
		e.Counts.Matching, e.Counts.Missing = 3, 1
	}
	return &obsDeploymentRuntimeBuilds{Advisory: true, State: e.State, Reason: e.Reason, Evidence: &e}
}

func newInt(value int) *int { return &value }

func TestBuildHistoryRecoveryGapsRegressionsAndOwnedViews(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var views []*obsDeploymentRuntimeBuilds
	for i, state := range []string{"mixed", "mixed", "unknown", "matching", "matching", "incomplete", "mixed", "matching"} {
		views = append(views, r.observe(buildHistoryCurrent(t, now.Add(time.Duration(i)*time.Second), state), now.Add(time.Duration(i)*time.Second)))
	}
	last := views[len(views)-1]
	p := last.Progress
	if p.Observations != 8 || p.MatchingThroughoutObservedPolls || !p.ObservedIncompleteEvidence || !p.ObservedSourceUnavailable || p.Regressions != 1 || !p.FirstMismatchAt.Equal(now) || !p.LastMismatchAt.Equal(now.Add(6*time.Second)) || !p.FirstMatchingAt.Equal(now.Add(3*time.Second)) || !p.LastRecoveryAt.Equal(now.Add(7*time.Second)) || !p.LastRegressionAt.Equal(now.Add(6*time.Second)) || last.History.TotalEvents != 6 {
		t.Fatalf("history did not preserve observed identity changes: %+v history=%+v", p, last.History)
	}
	if views[0].Progress.Observations != 1 || views[0].History.TotalEvents != 1 || views[3].Progress.Regressions != 0 || views[3].History.TotalEvents != 3 {
		t.Fatal("a later poll rewrote a saved view")
	}
	last.History.Events[0].Jobs[0].Expected = 999
	last.History.Events[0].Counts.Matching = 999
	*last.Progress.FirstMismatchAt = now.Add(-time.Hour)
	next := r.observe(buildHistoryCurrent(t, now.Add(8*time.Second), "matching"), now.Add(8*time.Second))
	if next.History.Events[0].Jobs[0].Expected != 1 || next.History.Events[0].Counts.Matching != 3 || !next.Progress.FirstMismatchAt.Equal(now) {
		t.Fatal("saved views alias recorder data")
	}
	var human bytes.Buffer
	writeObsDeploymentRuntimeBuilds(&human, next)
	if !strings.Contains(human.String(), "matching_throughout_observed_polls=false") || !strings.Contains(human.String(), "first_mismatch_at=") || !strings.Contains(human.String(), "envelope_omitted_events=0") {
		t.Fatal("human report hid earlier mismatches", human.String())
	}
	encoded, _ := json.Marshal(next.History)
	if strings.Contains(string(encoded), "git_sha") || strings.Contains(string(encoded), "target_id") || strings.Contains(string(encoded), "version") {
		t.Fatal("aggregate history retained per-target identity")
	}
}

func TestBuildHistoryAggregateDeduplicationAndClockRegression(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	first := r.observe(buildHistoryCurrent(t, now, "matching"), now)
	current := buildHistoryCurrent(t, now.Add(time.Second), "matching")
	current.Evidence.Items[0].Version = "changed-preview-only"
	current.Evidence.OmittedItems = 50
	second := r.observe(current, now.Add(time.Second))
	if first.History.TotalEvents != 1 || second.History.TotalEvents != 1 || second.Progress.Observations != 2 || !second.Progress.MatchingThroughoutObservedPolls {
		t.Fatal("sample clocks/preview churn filled aggregate history")
	}
	current.Evidence.Counts.Matching++
	current.Evidence.Counts.Observed++
	third := r.observe(current, now.Add(-time.Hour))
	if third.History.TotalEvents != 2 || !third.Progress.LastObservedAt.Equal(now.Add(time.Second)) || third.History.Events[1].At.Before(third.History.Events[0].At) {
		t.Fatal("aggregate counts or regressing coordinator clock lost history")
	}
	current.Evidence.Jobs[0].State, current.Evidence.Jobs[0].Reason = "unknown", "inventory_unavailable"
	current.State, current.Reason = "incomplete", "incomplete_identity_evidence"
	fourth := r.observe(current, now.Add(2*time.Second))
	if fourth.History.TotalEvents != 3 || !fourth.Progress.ObservedSourceUnavailable {
		t.Fatal("job availability change did not produce an event")
	}
	unknown := buildHistoryCurrent(t, now, "unknown")
	r.observe(unknown, now.Add(3*time.Second))
	unknown.SourceHTTPStatus = newInt(502)
	changed := r.observe(unknown, now.Add(4*time.Second))
	if changed.History.TotalEvents != 5 || *changed.History.Events[len(changed.History.Events)-1].SourceHTTPStatus != 502 {
		t.Fatal("source status change was deduplicated")
	}
	unknown.SourceHTTPStatus = newInt(429)
	unknown.Reason = "source_rate_limited"
	r.observe(unknown, now.Add(5*time.Second))
	unknown.Reason = "source_runtime_busy"
	changed = r.observe(unknown, now.Add(6*time.Second))
	events := changed.History.Events
	if changed.History.TotalEvents != 7 || len(events) < 2 || events[len(events)-2].Reason != "source_rate_limited" || events[len(events)-1].Reason != "source_runtime_busy" {
		t.Fatal("different 429 reasons were deduplicated")
	}
}

func TestBuildHistoryCountAndByteRetention(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		current := buildHistoryCurrent(t, now, "unknown")
		current.SourceHTTPStatus = newInt(500 + i%2)
		out = r.observe(current, now.Add(time.Duration(i)*time.Second))
	}
	if out.History.TotalEvents != 80 || out.History.RetainedEvents != api.ObsPostDeployBuildEventsMax || out.History.DroppedEvents != 80-api.ObsPostDeployBuildEventsMax || !out.History.Truncated || out.History.Events[0].Observation != 65 || out.Progress.Observations != 80 {
		t.Fatalf("count retention: %+v", out.History)
	}
	r = newObsDeploymentBuildRecorder()
	for i := range 80 {
		current := buildHistoryCurrent(t, now, "matching")
		// Six fixed source records exercise the larger supported role shape.
		current.Evidence.ExpectedRole = "control-plane-v1"
		current.Evidence.Jobs = append(current.Evidence.Jobs[:3], api.ObsRuntimeBuildJob{Job: "gatewayd-internal", Daemon: "gatewayd-internal", InventorySource: "active_configured_compute_registry", State: "available", Expected: 1}, api.ObsRuntimeBuildJob{Job: "schedd-compute", Daemon: "schedd", InventorySource: "active_configured_compute_registry", State: "available", Expected: 1}, api.ObsRuntimeBuildJob{Job: "vmmd", Daemon: "vmmd", InventorySource: "active_configured_compute_registry", State: "available", Expected: 1})
		current.Evidence.Counts.Expected = 6
		current.Evidence.Counts.Observed, current.Evidence.Counts.Matching, current.Evidence.OmittedItems = 6+i, 6+i, 2+i
		out = r.observe(current, now.Add(time.Duration(i)*time.Second))
	}
	if out.History.RetainedEvents >= api.ObsPostDeployBuildEventsMax || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax || out.History.RetainedBytes != obsDeploymentBuildHistoryBytes(out.History.Events) || out.History.TotalEvents != out.History.RetainedEvents+out.History.DroppedEvents || out.Progress.FirstMatchingAt == nil {
		t.Fatalf("byte retention: %+v", out.History)
	}
}

func TestBuildHistoryRunPreservesHealthExitAndTerminalObservationCount(t *testing.T) {
	for _, terminal := range []string{"passed", "cancelled", "write_failed"} {
		t.Run(terminal, func(t *testing.T) {
			now := time.Now().UTC()
			start := now
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Minute, poll: time.Second, expectedRole: "single-box-v1"}
			mixed := buildHistoryCurrent(t, now, "mixed")
			matching := buildHistoryCurrent(t, now, "matching")
			mixed.Evidence.EvidenceAfter, matching.Evidence.EvidenceAfter = opts.after, opts.after
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads, saves := 0, 0
			var initial obsDeploymentReport
			fetch := func(context.Context) obsDeploymentSnapshot {
				reads++
				snapshot := deploymentSnapshotTest(now)
				current := mixed
				if reads > 1 {
					current = matching
				}
				snapshot.runtimeBuilds = current.Evidence
				if reads == 1 && terminal != "write_failed" {
					snapshot.serving.err = obsFetchHTTPFailure("serving", 503)
				}
				return snapshot
			}
			report, code := runObsDeploymentVerification(ctx, opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error {
				now = now.Add(time.Second)
				if terminal == "cancelled" {
					cancel()
					return context.Canceled
				}
				return nil
			}, func(r obsDeploymentReport) error {
				saves++
				if saves == 1 {
					initial = r
				}
				if terminal == "write_failed" && saves == 2 {
					return errors.New("private write failure")
				}
				return nil
			})
			wantCode, wantReads := 0, 2
			if terminal == "cancelled" {
				wantCode, wantReads = 3, 1
			}
			if terminal == "write_failed" {
				wantCode, wantReads = 2, 1
			}
			if code != wantCode || reads != wantReads || report.HadConfirmedFaults || report.RuntimeBuilds.Progress.Observations != reads || initial.RuntimeBuilds.Progress.Observations != 0 || initial.RuntimeBuilds.History.TotalEvents != 0 || report.RuntimeBuilds.Progress.FirstMismatchAt == nil || !report.RuntimeBuilds.Progress.FirstMismatchAt.Equal(start) {
				t.Fatalf("terminal=%s code=%d reads=%d report=%+v", terminal, code, reads, report.RuntimeBuilds)
			}
			if terminal == "passed" && (report.State != "passed" || report.RuntimeBuilds.History.TotalEvents != 2 || report.RuntimeBuilds.Progress.LastRecoveryAt == nil) {
				t.Fatal("history changed health qualification")
			}
		})
	}
}

func TestBuildHistoryEnvelopeYieldsBeforePrimaryEvidenceAndOwnsSnapshots(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var current *obsDeploymentRuntimeBuilds
	for i := range 20 {
		current = r.observe(buildHistoryCurrent(t, now, "unknown"), now.Add(time.Duration(i)*time.Second))
		if i%2 == 0 {
			current = r.observe(buildHistoryCurrent(t, now, "mixed"), now.Add(time.Duration(i)*time.Second))
		}
	}
	report, _ := deploymentEnvelopeFaultFixture(t)
	report.RuntimeBuilds = current
	minimal := report
	minimal.RuntimeBuilds = &obsDeploymentRuntimeBuilds{Advisory: current.Advisory, State: current.State, Reason: current.Reason, SourceHTTPStatus: current.SourceHTTPStatus, Progress: current.Progress, History: cloneObsDeploymentBuildHistory(current.History)}
	minimal.RuntimeBuilds.History.EnvelopeOmittedEvents = len(current.History.Events)
	minimal.RuntimeBuilds.History.Events = []obsDeploymentBuildEvent{}
	minimal.RuntimeBuilds.History.account()
	body, _ := json.MarshalIndent(minimal, "", "  ")
	before, _ := json.MarshalIndent(report, "", "  ")
	fitted := fitObsDeploymentReportWithin(report, len(body)+1)
	got, _ := json.MarshalIndent(fitted, "", "  ")
	after, _ := json.MarshalIndent(report, "", "  ")
	if len(got) > len(body) || fitted.RuntimeBuilds.History.RetainedEvents != 0 || fitted.RuntimeBuilds.History.EnvelopeOmittedEvents != current.History.RetainedEvents || fitted.RuntimeBuilds.History.TotalEvents != current.History.TotalEvents || fitted.RuntimeBuilds.Progress.FirstMismatchAt == nil || !bytes.Equal(before, after) {
		t.Fatal("history fitting dropped summaries or mutated recorder snapshots", fmt.Sprint(len(got), len(body)))
	}
	if fitted.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots != report.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots || fitted.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots != report.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots {
		t.Fatal("primary fault evidence yielded before build history")
	}
	full := r.snapshot(buildHistoryCurrent(t, now, "unknown"))
	if full.History.EnvelopeOmittedEvents != 0 || full.History.RetainedEvents == 0 {
		t.Fatal("envelope permanently evicted recorder events")
	}
}
