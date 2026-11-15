package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentFaultEvidenceFixture(now time.Time, code string, pairs int) obsDeploymentSnapshot {
	snapshot := deploymentSnapshotTest(now)
	deploymentProbeTestState(&snapshot, "failing", "probe_failing")
	template := snapshot.serving.value.Probes[0]
	snapshot.serving.value.Probes = make([]api.ObsServingProbe, pairs)
	for i := range snapshot.serving.value.Probes {
		probe := template
		probe.URLID = fmt.Sprintf("%064x", i)
		attempts, status, recovered := 2, 0, false
		if code == "body_mismatch" || code == "latency" {
			status = 200
		}
		probe.Diagnostics = &api.ObsServingProbeDiagnostics{State: "available", EvaluatedAt: probe.EvaluatedAt, FailureCode: code, FirstFailureCode: code, Attempts: &attempts, HTTPStatus: &status, RecoveredOnRetry: &recovered}
		snapshot.serving.value.Probes[i] = probe
	}
	expected := pairs
	snapshot.serving.value.Inventory.ExpectedPairs = &expected
	counts := operatorobs.ServingProbeCounts(snapshot.serving.value.Probes)
	snapshot.serving.value.Counts = &counts
	return snapshot
}

func TestObsDeploymentProbeEvidenceSurvivesRecoveryAndLostTelemetry(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	now, calls := started, 0
	var saved []obsDeploymentReport
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: started.Add(-90 * time.Second), wait: time.Minute, poll: time.Second}
	fetch := func(context.Context) obsDeploymentSnapshot {
		calls++
		code := "tls"
		if calls >= 3 {
			code = "dns"
		}
		snapshot := deploymentFaultEvidenceFixture(now, code, 1)
		if calls == 4 {
			snapshot.serving.err = errors.New("https://private-provider.example/token")
		}
		if calls == 5 {
			snapshot = deploymentSnapshotTest(now)
		}
		return snapshot
	}
	report, code := runObsDeploymentVerification(context.Background(), opts, fetch, func() time.Time { return now }, func(_ context.Context, pause time.Duration) error { now = now.Add(pause); return nil }, func(report obsDeploymentReport) error { saved = append(saved, report); return nil })
	if code != 0 || report.State != "passed" || !report.HadConfirmedFaults || report.Attempts != 5 {
		t.Fatalf("evidence changed convergence semantics: exit=%d %+v", code, report)
	}
	var transitions []obsDeploymentTransition
	for _, transition := range report.Timeline.Transitions {
		if transition.Check == "serving_probes" {
			transitions = append(transitions, transition)
		}
	}
	if len(transitions) != 4 || transitions[0].Attempt != 1 || transitions[1].Attempt != 3 || transitions[2].State != "unknown" || transitions[3].State != "passed" {
		t.Fatalf("cause changes or duplicate clock updates mishandled: %+v", transitions)
	}
	first, second := transitions[0].ProbeFailures, transitions[1].ProbeFailures
	if first.Items[0].Diagnostics.FailureCode != "tls" || second.Items[0].Diagnostics.FailureCode != "dns" || first.CauseSHA256 == second.CauseSHA256 {
		t.Fatal("recovery overwrote the earlier diagnostic cause")
	}
	if !first.Items[0].EvaluatedAt.Equal(started) || !first.Items[0].LastRunAt.Equal(started.Add(-time.Minute)) || transitions[2].ProbeFailures != nil || transitions[3].ProbeFailures != nil {
		t.Fatal("lost evidence or recovery was presented as a current fault snapshot")
	}
	if report.Serving.Probes[0].Diagnostics != nil || saved[4].Timeline.Transitions[0].ProbeFailures.Items[0].Diagnostics.FailureCode != "tls" {
		t.Fatal("earlier evidence depended on the latest source payload")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, report)
	for _, want := range []string{"probe_evidence retained_items=2", "failure_code=tls", "failure_code=dns", "unrecovered_fault=false"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human history missing %q: %s", want, &human)
		}
	}
	body, err := json.Marshal(report)
	if err != nil || strings.Contains(string(body), "private-provider") || strings.Contains(human.String(), "private-provider") {
		t.Fatal("historical evidence leaked source error text")
	}
}

func TestObsDeploymentProbeEvidenceOwnsPointersAndFiltersUnconfirmedFaults(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := deploymentFaultEvidenceFixture(now, "tls", 1)
	checks, serving, _ := assessObsDeployment(snapshot, now.Add(-30*time.Second), now)
	failures := obsDeploymentProbeFailures(checks, serving, now.Add(-30*time.Second), now)
	recorder := newObsDeploymentTimeline(checks)
	history := recorder.observeWithProbeFailures(checks, now, 1, failures)
	*serving.Probes[0].Diagnostics.Attempts = 99
	*serving.Probes[0].Diagnostics.HTTPStatus = 503
	*serving.Probes[0].Diagnostics.EvaluatedAt = now.Add(time.Hour)
	*serving.Probes[0].LastRunAt = now.Add(time.Hour)
	serving.Probes[0].Diagnostics.FailureCode = "private-source-error"
	item := history.Transitions[0].ProbeFailures.Items[0]
	if *item.Diagnostics.Attempts != 2 || *item.Diagnostics.HTTPStatus != 0 || !item.Diagnostics.EvaluatedAt.Equal(now) || !item.LastRunAt.Equal(now.Add(-time.Minute)) || item.Diagnostics.FailureCode != "tls" {
		t.Fatal("a later source mutation rewrote retained evidence")
	}
	filtered := deploymentFaultEvidenceFixture(now, "tls", 4)
	old := now.Add(-45 * time.Second)
	filtered.serving.value.Probes[1].EvaluatedAt = &old
	filtered.serving.value.Probes[1].Diagnostics = nil
	filtered.serving.value.Probes[2].URLID = "https://private.example/query-token"
	missing := &filtered.serving.value.Probes[3]
	no := false
	missing.State, missing.Reason, missing.Reporting, missing.LastRunAt, missing.Diagnostics = "missing", "result_missing", &no, nil, nil
	failures = obsDeploymentProbeFailures(checks, &filtered.serving.value, now.Add(-30*time.Second), now)
	if failures.total != 2 || len(failures.preview) != 2 || failures.preview[1].Diagnostics.State != "unknown" || failures.preview[1].Diagnostics.Attempts != nil || failures.preview[1].Diagnostics.FailureCode != "" {
		t.Fatalf("unconfirmed or absent evidence became known: %+v", failures)
	}
	checks[0].State = "unknown"
	if unknown := obsDeploymentProbeFailures(checks, &filtered.serving.value, now.Add(-30*time.Second), now); unknown.total != 0 {
		t.Fatal("an unknown overall check supplied confirmed fault evidence")
	}
	body, _ := json.Marshal(failures.preview)
	if strings.Contains(string(body), "private") {
		t.Fatal("unsafe source identity entered a retained preview")
	}
}

func TestObsDeploymentProbeEvidenceDetectsChangesOutsidePreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := deploymentFaultEvidenceFixture(now, "tls", 10)
	checks, serving, _ := assessObsDeployment(snapshot, now.Add(-90*time.Second), now)
	first := obsDeploymentProbeFailures(checks, serving, now.Add(-90*time.Second), now)
	recorder := newObsDeploymentTimeline(checks)
	recorder.observeWithProbeFailures(checks, now, 1, first)
	serving.Probes[9].Diagnostics.FailureCode, serving.Probes[9].Diagnostics.FirstFailureCode = "dns", "dns"
	second := obsDeploymentProbeFailures(checks, serving, now.Add(-90*time.Second), now)
	history := recorder.observeWithProbeFailures(checks, now, 2, second)
	if len(history.Transitions) != 4 || first.fingerprint == second.fingerprint {
		t.Fatal("a cause change outside the preview was hidden")
	}
	capture := history.Transitions[3].ProbeFailures
	if capture.Total != 10 || len(capture.Items) != api.ObsPostDeployProbeEvidencePerTransitionMax || capture.Omitted != 6 || !capture.Truncated {
		t.Fatalf("preview omitted faults silently: %+v", capture)
	}
	if capture.Items[0].Diagnostics.FailureCode != "tls" {
		t.Fatal("the bounded preview was replaced with an unreviewed selection")
	}
}

func TestObsDeploymentProbeEvidenceCountBytesAndRollover(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	check := obsDeploymentCheck{Name: "serving_probes", State: "degraded", Reason: "serving_probe_fault"}
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	var history, first *obsDeploymentTimeline
	for attempt := 1; attempt <= api.ObsPostDeployTransitionsMax+90; attempt++ {
		now := started.Add(time.Duration(attempt) * time.Second)
		snapshot := deploymentFaultEvidenceFixture(now, "tls", 1)
		snapshot.serving.value.Probes[0].Node = "n"
		snapshot.serving.value.Probes[0].URLID = fmt.Sprintf("%064x", attempt)
		snapshot.serving.value.Probes[0].Diagnostics = nil // Smaller legacy evidence reaches the item cap first.
		failures := obsDeploymentProbeFailures([]obsDeploymentCheck{check}, &snapshot.serving.value, started.Add(-90*time.Second), now)
		history = recorder.observeWithProbeFailures([]obsDeploymentCheck{check}, now, attempt, failures)
		if attempt == 1 {
			first = history
		}
		if attempt == 200 {
			if history.ProbeEvidence.RetainedItems != api.ObsPostDeployProbeEvidenceItemsMax || history.ProbeEvidence.DroppedSnapshots == 0 {
				t.Fatalf("count or byte cap was not exercised: %+v", history.ProbeEvidence)
			}
		}
	}
	retention := history.ProbeEvidence
	if !retention.Truncated || retention.RetainedItems <= 0 || retention.RetainedItems > api.ObsPostDeployProbeEvidenceItemsMax || retention.RetainedBytes > api.ObsPostDeployProbeEvidenceBytesMax || history.DroppedTransitions != 90 {
		t.Fatalf("unbounded evidence or lost rollover accounting: %+v", history)
	}
	retained := 0
	for _, transition := range history.Transitions {
		if transition.ProbeFailures != nil {
			retained += len(transition.ProbeFailures.Items)
		}
	}
	if retained != retention.RetainedItems || first.ProbeEvidence.DroppedItems != 0 || first.Transitions[0].ProbeFailures.Items[0].URLID != fmt.Sprintf("%064x", 1) {
		t.Fatal("rollover corrupted retained counts or an earlier snapshot")
	}
	without := *history
	without.Transitions = slices.Clone(history.Transitions)
	for i := range without.Transitions {
		without.Transitions[i].ProbeFailures = nil
	}
	body, err := json.MarshalIndent(map[string]any{"timeline": history}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	base, err := json.MarshalIndent(map[string]any{"timeline": &without}, "", "  ")
	if err != nil || len(body)-len(base) != retention.RetainedBytes {
		t.Fatalf("byte accounting differs from serialized report: actual=%d tracked=%d err=%v", len(body)-len(base), retention.RetainedBytes, err)
	}
}

func TestObsDeploymentProbeEvidenceCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancelled", "degraded", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			started := time.Now().UTC().Truncate(time.Second)
			now, calls := started, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: started.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				snapshot := deploymentFaultEvidenceFixture(now, "tls", 1)
				if mode == "unknown" && calls > 1 {
					snapshot.serving.err = errors.New("private error")
				}
				return snapshot
			}, func() time.Time { return now }, func(_ context.Context, pause time.Duration) error {
				now = now.Add(pause)
				if mode == "cancelled" {
					cancel()
					return ctx.Err()
				}
				return nil
			}, func(obsDeploymentReport) error { return nil })
			want := 3
			if mode == "degraded" {
				want = 1
			}
			if code != want || report.Timeline.Transitions[0].ProbeFailures.Items[0].Diagnostics.FailureCode != "tls" || !report.Timeline.Checks[0].UnrecoveredFault {
				t.Fatalf("terminal outcome lost evidence or invented recovery: exit=%d %+v", code, report)
			}
		})
	}
}
