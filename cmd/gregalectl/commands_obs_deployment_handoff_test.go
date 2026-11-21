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

func deploymentHandoffFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeHandoffHealth {
	now := e.GeneratedAt
	attempt, completed := now.Add(-5*time.Second), now.Add(-4*time.Second)
	item := operatorobs.RuntimeHandoffItem{State: "available", ScrapeObservedAt: &now,
		Collection: operatorobs.RuntimeHandoffCollection{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "available", ObservedAt: &now}, Values: &operatorobs.RuntimeHandoffCollectionValues{Success: true, LastSuccessAt: &now}},
		Backlog:    operatorobs.RuntimeHandoffBacklog{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimeHandoffBacklogValues{}},
		Recovery:   operatorobs.RuntimeHandoffRecovery{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "waiting", ObservedAt: &now}, Values: &operatorobs.RuntimeHandoffRecoveryValues{Enabled: true, LastAttemptAt: &attempt, LastCompletedAt: &completed}}}
	for _, target := range e.Items {
		if target.Job == "imaged" {
			item.TargetID, item.Node = target.TargetID, target.Node
		}
	}
	h := &operatorobs.RuntimeHandoffHealth{RuntimeHandoffSummary: operatorobs.RuntimeHandoffSummary{State: "empty", Counts: operatorobs.RuntimeHandoffCounts{Expected: 1, Observed: 1, Complete: 1, EmptyScopes: 1, WaitingRecoveryScopes: 1}}, Items: []operatorobs.RuntimeHandoffItem{item}}
	if state == "waiting" || state == "partial" {
		finished := now.Add(-10 * time.Minute)
		h.State, h.Reason = "waiting", "pending_handoffs_observed"
		h.Counts.WaitingScopes, h.Counts.EmptyScopes = 1, 0
		h.Items[0].Backlog.State = "waiting"
		h.Items[0].Backlog.Values = &operatorobs.RuntimeHandoffBacklogValues{Pending: 100000, OldestFinishedAt: &finished, OldestWaitSeconds: 600}
	}
	switch state {
	case "partial":
		h.Incomplete, h.Counts.Complete, h.Counts.Unavailable, h.Counts.WaitingRecoveryScopes = true, 0, 1, 0
		h.Items[0].State = "unavailable"
		h.Items[0].Recovery = operatorobs.RuntimeHandoffRecovery{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "invalid_value"}}
	case "collection_failed":
		h.State, h.Reason, h.Incomplete = "incomplete", "incomplete_handoff_evidence", true
		h.Counts.Complete, h.Counts.Unavailable, h.Counts.EmptyScopes, h.Counts.UnsuccessfulCollections = 0, 1, 0, 1
		h.Items[0].State, h.Items[0].Collection.Values.Success = "unavailable", false
		h.Items[0].Backlog = operatorobs.RuntimeHandoffBacklog{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "collection_failed"}}
	case "stopping":
		h.Items[0].Recovery.State, h.Items[0].Recovery.Values.Enabled = "stopping", false
		h.Counts.WaitingRecoveryScopes, h.Counts.StoppingRecoveryScopes = 0, 1
	case "working":
		attempt, completed = now.Add(-time.Hour), now.Add(-2*time.Hour)
		h.Items[0].Recovery.State, h.Items[0].Recovery.Values.Inflight = "working", true
		h.Counts.WaitingRecoveryScopes, h.Counts.WorkingRecoveryScopes = 0, 1
	}
	return h
}

func TestHandoffEvidenceAdvisoryAndMalformedJSONIsolated(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"empty", "waiting", "partial", "collection_failed", "stopping", "working", "old_api", "missing_success", "null_pending", "missing_enabled", "stale_collection", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				state := fault
				if fault == "missing_success" {
					state = "collection_failed"
				}
				if fault == "missing_enabled" {
					state = "stopping"
				}
				base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				h := deploymentHandoffFixture(base, state)
				if !operatorobs.RuntimeHandoffHealthValid(base, h) {
					t.Fatal("invalid handoff fixture")
				}
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), HandoffHealth: h})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				item := raw["handoff_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "handoff_health")
				case "missing_success":
					delete(item["collection"].(map[string]any)["values"].(map[string]any), "success")
				case "null_pending":
					item["backlog"].(map[string]any)["values"].(map[string]any)["pending"] = nil
				case "missing_enabled":
					delete(item["recovery"].(map[string]any)["values"].(map[string]any), "enabled")
				case "stale_collection":
					item["collection"].(map[string]any)["values"].(map[string]any)["last_success_at"] = now.Add(-3 * time.Minute).Format(time.RFC3339)
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer handoff-private-token" {
						t.Error("unprotected handoff read")
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
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "handoff-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("handoff evidence added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.BuilderHealth == nil || report.RuntimeBuilds.LoopHealth == nil || report.RuntimeBuilds.Evidence.Readiness.State != "ready" {
					t.Fatal("handoff evidence changed health or other sections")
				}
				missing := fault == "old_api" || fault == "missing_success" || fault == "null_pending" || fault == "missing_enabled" || fault == "stale_collection"
				if (report.RuntimeBuilds.HandoffHealth == nil) != missing {
					t.Fatal("malformed optional handoff evidence was not isolated")
				}
				var human bytes.Buffer
				writeObsDeploymentRuntimeBuilds(&human, report.RuntimeBuilds)
				if strings.Contains(human.String(), "private-provider-error") {
					t.Fatal("handoff source data leaked")
				}
			})
		}
	}
}

func TestHandoffHistoryClippingGapsAndOwnedViews(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		base := deploymentRuntimeBuildProfileTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		state := "empty"
		if i%2 == 1 {
			state = "waiting"
		}
		if i == 3 {
			state = "gap"
		}
		if i == 5 {
			state = "partial"
		}
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if state != "gap" {
			current.HandoffHealth = deploymentHandoffFixture(base, state)
		}
		out = r.observe(current, at)
	}
	p := out.Progress.HandoffHealth
	if !p.FirstWaitingAt.Equal(now.Add(time.Second)) || !p.LastWaitingAt.Equal(now.Add(79*time.Second)) || p.WaitingTransitions != 39 || p.EmptyThroughoutObservedPolls || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatalf("handoff history lost bounded evidence: %+v history=%+v", p, out.History)
	}
	out.History.Events[0].HandoffHealth.Counts.Expected = 999
	*p.FirstWaitingAt = now.Add(-time.Hour)
	at := now.Add(80 * time.Second)
	base := deploymentRuntimeBuildProfileTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	next := r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, HandoffHealth: deploymentHandoffFixture(base, "empty")}, at)
	if next.History.Events[0].HandoffHealth.Counts.Expected == 999 || !next.Progress.HandoffHealth.FirstWaitingAt.Equal(now.Add(time.Second)) {
		t.Fatal("returned history aliased recorder state")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-500)
	if fitted.RuntimeBuilds.HandoffEnvelopeOmittedItems != 1 || fitted.RuntimeBuilds.HandoffHealth.Counts != next.HandoffHealth.Counts || len(next.HandoffHealth.Items) != 1 {
		t.Fatal("handoff report fitting lost counts or mutated inputs")
	}
	for i := range 3 {
		at := now.Add(time.Duration(i) * time.Second)
		b := deploymentRuntimeBuildProfileTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		v := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &b, HandoffHealth: deploymentHandoffFixture(b, "waiting")}
		if i == 0 {
			r = newObsDeploymentBuildRecorder()
		}
		out = r.observe(v, at)
	}
	if out.History.TotalEvents != 1 {
		t.Fatal("routine handoff clock churn consumed history")
	}
}
