package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentBuildRecoveryBaseTest(t *testing.T, now, after time.Time, release, role string) api.ObsRuntimeBuilds {
	t.Helper()
	base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
	for i := range base.Items {
		base.Items[i].TargetID = fmt.Sprintf("%064x", i+1)
	}
	return base
}

func deploymentBuildRecoveryFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeBuildRecoveryHealth {
	now := e.GeneratedAt
	signal := operatorobs.RuntimeHandoffSignal{State: "available", ObservedAt: &now}
	item := operatorobs.RuntimeBuildRecoveryItem{Job: "apid", State: "available", ScrapeObservedAt: &now, Collection: operatorobs.RuntimeHandoffCollection{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeHandoffCollectionValues{Success: true, LastSuccessAt: &now}}, Running: operatorobs.RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimeBuildRecoveryRunningValues{}}, Cleanup: operatorobs.RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimeBuildRecoveryCleanupValues{}}}
	for _, target := range e.Items {
		if target.Job == "apid" {
			item.TargetID, item.Node = target.TargetID, target.Node
		}
	}
	h := &operatorobs.RuntimeBuildRecoveryHealth{RuntimeBuildRecoverySummary: operatorobs.RuntimeBuildRecoverySummary{Scope: operatorobs.RuntimeBuildRecoveryScope, State: "empty", Counts: operatorobs.RuntimeBuildRecoveryCounts{Expected: 1, Observed: 1, Complete: 1, EmptyViews: 1}}, Items: []operatorobs.RuntimeBuildRecoveryItem{}}
	if state == "running" || state == "both" || state == "partial_running" {
		oldest, age := now.Add(-3*time.Hour), float64(10800)
		item.Running.State, item.Running.Values = "nonempty", &operatorobs.RuntimeBuildRecoveryRunningValues{Count: 2, OldestStartedAt: &oldest, OldestAgeSeconds: &age}
		h.Counts.RunningViews = 1
	}
	if state == "cleanup" || state == "nonempty" || state == "both" || state == "partial_cleanup" {
		oldest, age := now.Add(-24*time.Hour), float64(86400)
		item.Cleanup.State, item.Cleanup.Values = "nonempty", &operatorobs.RuntimeBuildRecoveryCleanupValues{Pending: 3, Retried: 2, OldestCreatedAt: &oldest, OldestAgeSeconds: &age}
		h.Counts.CleanupViews, h.Counts.RetriedCleanupViews = 1, 1
	}
	if h.Counts.RunningViews+h.Counts.CleanupViews > 0 {
		h.State, h.Reason = "nonempty", "build_recovery_evidence_observed"
		h.Counts.NonemptyViews, h.Counts.EmptyViews = 1, 0
	}
	if state == "partial_running" || state == "partial_cleanup" {
		h.Incomplete, h.Counts.Complete, h.Counts.Missing = true, 0, 1
		item.State = "missing"
		bad := operatorobs.RuntimeHandoffSignal{State: "missing", Reason: "metric_missing"}
		if state == "partial_running" {
			item.Cleanup = operatorobs.RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: bad}
		} else {
			item.Running = operatorobs.RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: bad}
		}
	}
	if state == "collection_failed" {
		h.State, h.Reason, h.Incomplete = "incomplete", "incomplete_build_recovery_evidence", true
		h.Counts.Complete, h.Counts.EmptyViews, h.Counts.Unavailable, h.Counts.UnsuccessfulCollections = 0, 0, 1, 1
		item.State, item.Collection.Values.Success = "unavailable", false
		bad := operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "collection_failed"}
		item.Running = operatorobs.RuntimeBuildRecoveryRunning{RuntimeHandoffSignal: bad}
		item.Cleanup = operatorobs.RuntimeBuildRecoveryCleanup{RuntimeHandoffSignal: bad}
	}
	h.Items = append(h.Items, item)
	return h
}

func TestBuildRecoveryEvidenceTransportAndMalformedIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"empty", "cleanup", "running", "both", "partial_running", "partial_cleanup", "collection_failed", "old_api", "missing_scope", "wrong_scope", "missing_success", "missing_retried", "missing_count", "missing_job", "wrong_job", "null_pending", "missing_known_age", "stale_collection", "invalid_subset", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				state := fault
				if fault == "missing_known_age" {
					state = "cleanup"
				}
				base := deploymentBuildRecoveryBaseTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				h := deploymentBuildRecoveryFixture(base, state)
				if !operatorobs.RuntimeBuildRecoveryHealthValid(base, h) {
					t.Fatal("invalid build_recovery fixture")
				}
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), HandoffHealth: deploymentHandoffFixture(base, "empty"), BuildRecoveryHealth: h, PostCaptureHealth: deploymentPostCaptureFixture(base, "empty"), SnapshotPrimeHealth: deploymentSnapshotPrimeFixture(base, "empty"), PreparationHealth: deploymentPreparationFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				item := raw["build_recovery_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				cleanup := item["cleanup"].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "build_recovery_health")
				case "missing_scope":
					delete(raw["build_recovery_health"].(map[string]any), "scope")
				case "wrong_scope":
					raw["build_recovery_health"].(map[string]any)["scope"] = "unique_deployments"
				case "missing_job":
					delete(item, "job")
				case "wrong_job":
					item["job"] = "schedd"
				case "missing_success":
					delete(item["collection"].(map[string]any)["values"].(map[string]any), "success")
				case "missing_retried":
					delete(cleanup["values"].(map[string]any), "retried")
				case "missing_count":
					delete(item["running"].(map[string]any)["values"].(map[string]any), "count")
				case "null_pending":
					cleanup["values"].(map[string]any)["pending"] = nil
				case "missing_known_age":
					delete(cleanup["values"].(map[string]any), "oldest_age_seconds")
				case "stale_collection":
					item["collection"].(map[string]any)["values"].(map[string]any)["last_success_at"] = now.Add(-3 * time.Minute).Format(time.RFC3339)
				case "invalid_subset":
					cleanup["values"].(map[string]any)["retried"] = 1
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer build_recovery-private-token" {
						t.Error("unprotected build_recovery read")
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
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "build_recovery-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("build_recovery added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.HandoffHealth == nil || report.RuntimeBuilds.BuilderHealth == nil || report.RuntimeBuilds.LoopHealth == nil || report.RuntimeBuilds.SnapshotPrimeHealth == nil || report.RuntimeBuilds.PreparationHealth == nil || report.RuntimeBuilds.PostCaptureHealth == nil {
					t.Fatal("build_recovery changed health or other sections")
				}
				missing := strings.HasPrefix(fault, "missing_") || fault == "old_api" || fault == "wrong_scope" || fault == "wrong_job" || fault == "null_pending" || fault == "stale_collection" || fault == "invalid_subset"
				if (report.RuntimeBuilds.BuildRecoveryHealth == nil) != missing {
					t.Fatal("malformed build_recovery was not isolated")
				}
				var human bytes.Buffer
				writeObsDeploymentRuntimeBuilds(&human, report.RuntimeBuilds)
				if strings.Contains(human.String(), "private-provider-error") {
					t.Fatal("raw source data leaked")
				}
			})
		}
	}
}

func TestBuildRecoveryHistoryClassTransitionsOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		base := deploymentBuildRecoveryBaseTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		state := "empty"
		if i%2 == 1 {
			state = "cleanup"
		}
		if i == 5 {
			state = "cleanup"
		}
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if i != 3 {
			current.BuildRecoveryHealth = deploymentBuildRecoveryFixture(base, state)
		}
		out = r.observe(current, at)
	}
	p := out.Progress.BuildRecoveryHealth
	if p.NonemptyTransitions != 39 || !p.ObservedIncompleteEvidence || p.EmptyThroughoutObservedPolls || !p.FirstNonemptyAt.Equal(now.Add(time.Second)) || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("bounded build_recovery history lost evidence")
	}
	out.History.Events[0].BuildRecoveryHealth.Counts.CleanupViews = 999
	*p.FirstNonemptyAt = now.Add(-time.Hour)
	base := deploymentBuildRecoveryBaseTest(t, now.Add(80*time.Second), now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, BuildRecoveryHealth: deploymentBuildRecoveryFixture(base, "nonempty")}
	next := r.observe(current, base.GeneratedAt)
	if next.History.Events[0].BuildRecoveryHealth.Counts.CleanupViews == 999 || !next.Progress.BuildRecoveryHealth.FirstNonemptyAt.Equal(now.Add(time.Second)) {
		t.Fatal("history aliased returned view")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.BuildRecoveryEnvelopeOmittedItems != 1 || len(next.BuildRecoveryHealth.Items) != 1 || fitted.RuntimeBuilds.BuildRecoveryHealth.Counts != next.BuildRecoveryHealth.Counts {
		t.Fatal("fitting changed aggregates or source")
	}
	// Component coverage changes must be events; routine clocks and ages must not.
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	for i := range 4 {
		copy := operatorobs.CloneRuntimeBuildRecoveryHealth(current.BuildRecoveryHealth)
		copy.Items[0].Cleanup.Values.Pending += int64(i)
		current.BuildRecoveryHealth = copy
		out = r.observe(current, base.GeneratedAt)
	}
	if out.History.TotalEvents != 1 {
		t.Fatal("routine age churn added history")
	}
	current.BuildRecoveryHealth = deploymentBuildRecoveryFixture(base, "both")
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 2 {
		t.Fatal("component coverage change missing from history")
	}
}
