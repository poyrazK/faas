package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentReadinessFixture(e *api.ObsRuntimeBuilds, state, reason string) {
	n := len(e.Items)
	counts := api.ObsRuntimeReadinessCounts{Ready: n}
	summaryState, summaryReason := "ready", ""
	if state != "ready" {
		counts.Ready = n - 1
		switch state {
		case "not_ready":
			counts.NotReady = 1
			summaryState, summaryReason = "not_ready", "fresh_not_ready"
		case "stale":
			counts.Stale = 1
			summaryState, summaryReason = "incomplete", "incomplete_readiness_evidence"
		}
	}
	e.Readiness = &api.ObsRuntimeReadiness{State: summaryState, Reason: summaryReason, Counts: counts, Reasons: []api.ObsRuntimeReadinessReason{{Reason: "ready", Count: counts.Ready}}}
	if state == "not_ready" {
		e.Readiness.Reasons = append(e.Readiness.Reasons, api.ObsRuntimeReadinessReason{Reason: reason, Count: 1})
	}
	for i := range e.Items {
		e.Items[i].Readiness = &api.ObsRuntimeReadinessItem{State: "ready", Reason: "ready", ObservedAt: e.Items[i].ObservedAt, ReasonObservedAt: e.Items[i].ObservedAt}
	}
	if state != "ready" {
		e.Items[0].Readiness.State, e.Items[0].Readiness.Reason = state, reason
	}
}

func TestReadinessHistoryLossRecoveryGapsAndOwnership(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i, state := range []string{"ready", "ready", "not_ready", "unknown", "not_ready", "ready", "stale", "not_ready", "ready"} {
		at := now.Add(time.Duration(i) * time.Second)
		current := buildHistoryCurrent(t, at, "matching")
		switch state {
		case "unknown":
			current.Evidence.Readiness = nil
		case "stale":
			deploymentReadinessFixture(current.Evidence, state, "readiness_stale")
		case "not_ready":
			deploymentReadinessFixture(current.Evidence, state, "draining")
		default:
			deploymentReadinessFixture(current.Evidence, state, "ready")
		}
		if state != "unknown" && !operatorobs.RuntimeReadinessValid(*current.Evidence) {
			t.Fatal("invalid fixture")
		}
		out = r.observe(current, at)
	}
	p := out.Progress.Readiness
	if p.Losses != 2 || !p.LastLossAt.Equal(now.Add(7*time.Second)) || !p.LastRecoveryAt.Equal(now.Add(8*time.Second)) || p.ReadyThroughoutObservedPolls || !p.ObservedIncompleteEvidence || out.Progress.Regressions != 0 || !out.Progress.MatchingThroughoutObservedPolls || out.History.TotalEvents != 8 {
		t.Fatalf("incorrect progress: %+v history=%+v", p, out.History)
	}
	// Current clocks and preview-only changes do not produce history events.
	next := buildHistoryCurrent(t, now.Add(9*time.Second), "matching")
	deploymentReadinessFixture(next.Evidence, "ready", "ready")
	out.History.Events[0].Readiness.Reasons[0].Count = 999
	*p.LastRecoveryAt = now.Add(-time.Hour)
	newer := r.observe(next, now.Add(9*time.Second))
	if newer.History.TotalEvents != 8 || newer.History.Events[0].Readiness.Reasons[0].Count == 999 || !newer.Progress.Readiness.LastRecoveryAt.Equal(now.Add(8*time.Second)) {
		t.Fatal("readiness history aliases caller or counts sample churn")
	}
	var human bytes.Buffer
	writeObsDeploymentRuntimeBuilds(&human, newer)
	if !strings.Contains(human.String(), "readiness_progress losses=2") || !strings.Contains(human.String(), "runtime_readiness advisory=true state=ready") {
		t.Fatal(human.String())
	}
	body, _ := json.Marshal(newer.History)
	if strings.Contains(string(body), "observed_at") || strings.Contains(string(body), "target_id") {
		t.Fatal("readiness history retained individual samples")
	}
}

func TestReadinessRemainsAdvisoryAndInvalidOptionalDataIsDropped(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-time.Minute), wait: time.Minute, poll: time.Second, expectedRole: "single-box-v2"}
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_ready", true: "invalid"}[invalid], func(t *testing.T) {
			e := deploymentRuntimeBuildProfileTest(t, now, opts.after, opts.release, opts.expectedRole)
			deploymentReadinessFixture(&e, "not_ready", "database")
			if invalid {
				e.Readiness.Reasons[1].Reason = "private-provider-error"
			}
			snapshot := deploymentSnapshotTest(now)
			snapshot.runtimeBuilds = &e
			report, code := runObsDeploymentVerification(t.Context(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("readiness added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
			if code != 0 || report.State != "passed" || report.HadConfirmedFaults || report.RuntimeBuilds.State != "matching" {
				t.Fatalf("readiness changed health: %d %+v", code, report)
			}
			if invalid != (report.RuntimeBuilds.Evidence.Readiness == nil) {
				t.Fatal("optional readiness sanitization failed")
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "private-provider-error") {
				t.Fatal("unreviewed reason leaked")
			}
		})
	}
}

func TestReadinessHistorySharesCountAndByteBounds(t *testing.T) {
	now := time.Now().UTC()
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 50 {
		current := buildHistoryCurrent(t, now, "matching")
		state, reason := "ready", "ready"
		if i%2 == 1 {
			state, reason = "not_ready", "draining"
		}
		deploymentReadinessFixture(current.Evidence, state, reason)
		out = r.observe(current, now.Add(time.Duration(i)*time.Second))
	}
	if out.History.TotalEvents != 50 || !out.History.Truncated || out.History.RetainedEvents > api.ObsPostDeployBuildEventsMax || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax || out.Progress.Readiness.Losses != 25 {
		t.Fatalf("unbounded history: %+v", out.History)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: out}, len(body)/2)
	if fitted.RuntimeBuilds.Progress.Readiness.Losses != 25 || fitted.RuntimeBuilds.History.EnvelopeOmittedEvents == 0 || out.History.EnvelopeOmittedEvents != 0 {
		t.Fatal("report fitting lost readiness progress or mutated history")
	}
}

func TestReadinessNotReadyWithUnavailableInventory(t *testing.T) {
	now := time.Now().UTC()
	current := buildHistoryCurrent(t, now, "matching")
	deploymentReadinessFixture(current.Evidence, "not_ready", "draining")
	// Known not-ready evidence takes priority while another job lacks inventory.
	current.Evidence.Jobs[0].State, current.Evidence.Jobs[0].Reason = "unknown", "inventory_unavailable"
	out := newObsDeploymentBuildRecorder().observe(current, now)
	if !out.Progress.Readiness.ObservedIncompleteEvidence || out.Progress.Readiness.FirstNotReadyAt == nil || out.Progress.Readiness.Losses != 0 {
		t.Fatalf("unknown inventory hid coverage gap: %+v", out.Progress.Readiness)
	}
}
