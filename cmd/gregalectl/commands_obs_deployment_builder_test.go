package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentBuilderFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeBuilderHealth {
	now := e.GeneratedAt
	started, attempt, completed := now.Add(-time.Hour), now.Add(-5*time.Second), now.Add(-4*time.Second)
	workerState := state
	if state == "partial" {
		workerState = "overdue"
	}
	v := &operatorobs.RuntimeBuilderWorkerValues{Enabled: true, StartedAt: started, LastAttemptAt: &attempt, LastCompletedAt: &completed, IntervalSeconds: 2, BusyAllowanceSeconds: 900, ProgressAgeSeconds: 4, ProgressAllowanceSeconds: 64}
	c := operatorobs.RuntimeBuilderCounts{Expected: 1, Observed: 1, Complete: 1}
	switch workerState {
	case "working":
		attempt, completed = now.Add(-10*time.Minute), now.Add(-11*time.Minute)
		v.Inflight, v.ProgressAgeSeconds, v.ProgressAllowanceSeconds, c.WorkingWorkers = true, 600, 964, 1
	case "overdue":
		attempt, completed = now.Add(-91*time.Second), now.Add(-90*time.Second)
		v.ProgressAgeSeconds, c.OverdueWorkers = 90, 1
	case "draining":
		v.Enabled, c.DrainingWorkers = false, 1
	default:
		c.WaitingWorkers = 1
	}
	item := operatorobs.RuntimeBuilderItem{State: "available", ScrapeObservedAt: &now,
		Worker: operatorobs.RuntimeBuilderWorker{RuntimeBuilderSignal: operatorobs.RuntimeBuilderSignal{State: workerState, ObservedAt: &now}, Values: v},
		Slots:  operatorobs.RuntimeBuilderSlots{RuntimeBuilderSignal: operatorobs.RuntimeBuilderSignal{State: "available", ObservedAt: &now}, Values: &operatorobs.RuntimeBuilderSlotValues{Limit: 1}},
		Queues: []operatorobs.RuntimeBuilderQueue{}}
	for _, target := range e.Items {
		if target.Job == "builderd" {
			item.Node, item.TargetID = target.Node, target.TargetID
		}
	}
	for _, q := range []string{"build", "cancel"} {
		item.Queues = append(item.Queues, operatorobs.RuntimeBuilderQueue{RuntimeBuilderSignal: operatorobs.RuntimeBuilderSignal{State: "available", ObservedAt: &now}, Queue: q, Values: &operatorobs.RuntimeBuilderQueueValues{Enabled: true, Capacity: 64, Workers: 2}})
	}
	h := &operatorobs.RuntimeBuilderHealth{RuntimeBuilderSummary: operatorobs.RuntimeBuilderSummary{State: "observed", Counts: c}, Items: []operatorobs.RuntimeBuilderItem{item}}
	if workerState == "overdue" {
		h.State, h.Reason = "overdue", "worker_progress_overdue"
	}
	if state == "partial" {
		h.Incomplete, h.Counts.Complete, h.Counts.Missing = true, 0, 1
		h.Items[0].State = "missing"
		h.Items[0].Slots = operatorobs.RuntimeBuilderSlots{RuntimeBuilderSignal: operatorobs.RuntimeBuilderSignal{State: "missing", Reason: "metric_missing"}}
	}
	return h
}

func TestBuilderEvidenceAdvisoryAndOptionalJSONIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"waiting", "working", "overdue", "draining", "partial", "old_api", "missing_flag", "null_slot", "bad_queue", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				state := fault
				if fault == "missing_flag" {
					state = "draining"
				}
				if fault == "old_api" || fault == "null_slot" || fault == "bad_queue" || fault == "unknown_fields" {
					state = "waiting"
				}
				h := deploymentBuilderFixture(base, state)
				if !operatorobs.RuntimeBuilderHealthValid(base, h) {
					t.Fatal("invalid builder fixture")
				}
				encoded, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: h})
				var raw map[string]any
				_ = json.Unmarshal(encoded, &raw)
				item := raw["builder_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "builder_health")
				case "missing_flag":
					delete(item["worker"].(map[string]any)["values"].(map[string]any), "enabled")
				case "null_slot":
					item["slots"].(map[string]any)["values"].(map[string]any)["used"] = nil
				case "bad_queue":
					item["queues"].([]any)[0].(map[string]any)["queue"] = "private-provider-error"
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer builder-private-token" {
						t.Error("unprotected builder read")
					}
					switch r.URL.Path {
					case operatorobs.RuntimeBuildsPath:
						_ = json.NewEncoder(w).Encode(raw)
					case operatorobs.ServingHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.serving.value)
					case operatorobs.NotificationHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.notifications)
					default:
						t.Error("unexpected read")
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_APID_URL", server.URL)
				opts := obsDeploymentOptions{release: release, after: after, expectedRole: role, wait: time.Minute, poll: time.Second}
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "builder-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("builder evidence added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.Evidence.Readiness.State != "ready" || report.RuntimeBuilds.LoopHealth == nil {
					t.Fatal("builder diagnostics changed health or other evidence")
				}
				missing := fault == "old_api" || fault == "missing_flag" || fault == "null_slot" || fault == "bad_queue"
				if (report.RuntimeBuilds.BuilderHealth == nil) != missing {
					t.Fatal("optional builder JSON was not isolated")
				}
				var human bytes.Buffer
				writeObsDeploymentRuntimeBuilds(&human, report.RuntimeBuilds)
				if strings.Contains(human.String(), "private-provider-error") {
					t.Fatal("raw builder data leaked")
				}
			})
		}
	}
}

func TestBuilderHistoryRecoveryDrainGapsAndOwnership(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i, state := range []string{"waiting", "waiting", "overdue", "draining", "gap", "partial", "working", "overdue", "waiting"} {
		at := now.Add(time.Duration(i) * time.Second)
		base := deploymentRuntimeBuildProfileTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if state != "gap" {
			current.BuilderHealth = deploymentBuilderFixture(base, state)
		}
		out = r.observe(current, at)
		if i == 3 && out.Progress.BuilderHealth.LastRecoveryAt != nil {
			t.Fatal("drain invented a recovery")
		}
	}
	p := out.Progress.BuilderHealth
	if p.OverdueRegressions != 2 || !p.LastRecoveryAt.Equal(now.Add(8*time.Second)) || !p.FirstOverdueAt.Equal(now.Add(2*time.Second)) || !p.LastOverdueAt.Equal(now.Add(7*time.Second)) || p.WithinAllowanceThroughoutObservedPolls || !p.ObservedIncompleteEvidence || out.History.TotalEvents != 8 {
		t.Fatalf("builder history lost observations: %+v history=%+v", p, out.History)
	}
	out.History.Events[0].BuilderHealth.Counts.Expected = 999
	*p.FirstOverdueAt = now.Add(-time.Hour)
	base := deploymentRuntimeBuildProfileTest(t, now.Add(9*time.Second), now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	next := r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, BuilderHealth: deploymentBuilderFixture(base, "waiting")}, now.Add(9*time.Second))
	if next.History.Events[0].BuilderHealth.Counts.Expected == 999 || !next.Progress.BuilderHealth.FirstOverdueAt.Equal(now.Add(2*time.Second)) {
		t.Fatal("returned builder history aliased recorder state")
	}
	body, _ := json.MarshalIndent(next.History, "", "  ")
	if strings.Contains(string(body), "progress_age_seconds") || strings.Contains(string(body), "oldest_wait_seconds") {
		t.Fatal("builder history retained clock/depth churn")
	}
	before, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(before)-1000)
	if fitted.RuntimeBuilds.BuilderEnvelopeOmittedItems != 1 || fitted.RuntimeBuilds.BuilderHealth.Counts != next.BuilderHealth.Counts || len(next.BuilderHealth.Items) != 1 || next.BuilderEnvelopeOmittedItems != 0 {
		t.Fatal("builder report fitting lost counts or mutated the saved view")
	}
}
