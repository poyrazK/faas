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

func deploymentAppTaskBaseTest(t *testing.T, now, after time.Time, release, role string) api.ObsRuntimeBuilds {
	t.Helper()
	base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
	for i := range base.Items {
		base.Items[i].TargetID = fmt.Sprintf("%064x", i+1)
	}
	return base
}

func deploymentAppTaskFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeAppTaskHealth {
	now := e.GeneratedAt
	signal := operatorobs.RuntimeHandoffSignal{State: "available", ObservedAt: &now}
	item := operatorobs.RuntimeAppTaskItem{Dispatch: operatorobs.RuntimeAppTaskDispatch{RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "running", ObservedAt: &now}, Values: &operatorobs.RuntimeAppTaskDispatchValues{Enabled: true, Running: true, SlotsConfigured: 4, LastClaimSuccessAt: &now, LastSweepSuccessAt: &now}}, State: "available", ScrapeObservedAt: &now, Collection: operatorobs.RuntimeHandoffCollection{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeHandoffCollectionValues{Success: true, LastSuccessAt: &now}}, Observation: operatorobs.RuntimeAppTaskObservation{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeAppTaskObservationValues{Enabled: true}}}
	h := &operatorobs.RuntimeAppTaskHealth{RuntimeAppTaskSummary: operatorobs.RuntimeAppTaskSummary{Scope: operatorobs.RuntimeAppTaskScope, State: "empty", Counts: operatorobs.RuntimeAppTaskCounts{Expected: 1, Observed: 1, Complete: 1, EmptyViews: 1, EnabledViews: 1, DispatchEnabledViews: 1, RunnerRunningViews: 1}}, Items: []operatorobs.RuntimeAppTaskItem{}}
	for _, key := range deploymentAppTaskKeys() {
		kind, name, _ := strings.Cut(key, "/")
		item.Classes = append(item.Classes, operatorobs.RuntimeAppTaskClass{Kind: kind, Class: name, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimeAppTaskClassValues{}})
		h.Classes = append(h.Classes, operatorobs.RuntimeAppTaskClassCounts{Kind: kind, Class: name, EmptyViews: 1})
	}
	if state == "nonempty" || state == "unknown_ages" || state == "partial" || state == "disabled" || state == "deadline_overdue" {
		h.State, h.Reason = "nonempty", "app_task_evidence_observed"
		h.Counts.NonemptyViews, h.Counts.EmptyViews = 1, 0
		h.Classes[0].NonemptyViews, h.Classes[0].EmptyViews = 1, 0
		oldest, age := now.Add(-time.Hour), float64(3600)
		item.Classes[0].State = "nonempty"
		item.Classes[0].Values = &operatorobs.RuntimeAppTaskClassValues{Pending: 3, OldestBoundaryAt: &oldest, OldestKnownAgeSeconds: &age}
		if state == "unknown_ages" {
			item.Classes[0].Values = &operatorobs.RuntimeAppTaskClassValues{Pending: 3, UnknownAge: 3}
			h.Counts.UnknownAgeViews, h.Classes[0].UnknownAgeViews = 1, 1
		}
	}
	if state == "deadline_overdue" {
		item.Classes[3].State, item.Classes[3].Values = item.Classes[0].State, item.Classes[0].Values
		item.Classes[0].State, item.Classes[0].Values = "empty", &operatorobs.RuntimeAppTaskClassValues{}
		h.Classes[3].NonemptyViews, h.Classes[3].EmptyViews = 1, 0
		h.Classes[0].NonemptyViews, h.Classes[0].EmptyViews = 0, 1
	}
	switch state {
	case "partial":
		h.Incomplete, h.Counts.Complete, h.Counts.Missing = true, 0, 1
		item.State = "missing"
		item.Classes[2] = operatorobs.RuntimeAppTaskClass{Kind: "release", Class: "held", RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "missing", Reason: "metric_missing"}}
		h.Classes[2].EmptyViews = 0
	case "disabled":
		item.Observation.Values.Enabled = false
		h.Counts.EnabledViews, h.Counts.DisabledViews = 0, 1
	case "collection_failed":
		h.State, h.Reason, h.Incomplete = "incomplete", "incomplete_app_task_evidence", true
		h.Counts.Complete, h.Counts.EmptyViews, h.Counts.Unavailable, h.Counts.UnsuccessfulCollections = 0, 0, 1, 1
		item.State, item.Collection.Values.Success = "unavailable", false
		for i := range item.Classes {
			item.Classes[i] = operatorobs.RuntimeAppTaskClass{Kind: h.Classes[i].Kind, Class: h.Classes[i].Class, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "collection_failed"}}
			h.Classes[i].EmptyViews = 0
		}
	}
	for _, target := range e.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		body, _ := json.Marshal(item)
		var copy operatorobs.RuntimeAppTaskItem
		_ = json.Unmarshal(body, &copy)
		copy.Job, copy.TargetID, copy.Node = target.Job, target.TargetID, target.Node
		h.Items = append(h.Items, copy)
	}
	body, _ := json.Marshal(h.Counts)
	var counts map[string]int
	_ = json.Unmarshal(body, &counts)
	for key := range counts {
		counts[key] *= len(h.Items)
	}
	counts["expected"] = 0
	for _, job := range e.Jobs {
		if job.Job == "schedd" || job.Job == "schedd-compute" {
			counts["expected"] += job.Expected
		}
	}
	body, _ = json.Marshal(counts)
	_ = json.Unmarshal(body, &h.Counts)
	for i := range h.Classes {
		h.Classes[i].NonemptyViews *= len(h.Items)
		h.Classes[i].EmptyViews *= len(h.Items)
		h.Classes[i].UnknownAgeViews *= len(h.Items)
	}
	return h
}

func TestAppTaskEvidenceTransportAndMalformedIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"empty", "nonempty", "deadline_overdue", "unknown_ages", "partial", "disabled", "collection_failed", "old_api", "missing_scope", "wrong_scope", "missing_success", "missing_enabled", "missing_unknown_age", "missing_job", "wrong_job", "null_pending", "missing_known_age", "missing_kind", "missing_running", "missing_slots", "invalid_slots", "bad_kind", "stale_collection", "bad_class_order", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				state := fault
				if fault == "missing_known_age" {
					state = "nonempty"
				}
				base := deploymentAppTaskBaseTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				h := deploymentAppTaskFixture(base, state)
				if !operatorobs.RuntimeAppTaskHealthValid(base, h) {
					t.Fatal("invalid app_task fixture")
				}
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), HandoffHealth: deploymentHandoffFixture(base, "empty"), AppTaskHealth: h, PostCaptureHealth: deploymentPostCaptureFixture(base, "empty"), BuildRecoveryHealth: deploymentBuildRecoveryFixture(base, "empty"), SnapshotPrimeHealth: deploymentSnapshotPrimeFixture(base, "empty"), PreparationHealth: deploymentPreparationFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				item := raw["app_task_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				class := item["classes"].([]any)[0].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "app_task_health")
				case "missing_scope":
					delete(raw["app_task_health"].(map[string]any), "scope")
				case "wrong_scope":
					raw["app_task_health"].(map[string]any)["scope"] = "unique_deployments"
				case "missing_job":
					delete(item, "job")
				case "wrong_job":
					item["job"] = "imaged"
				case "missing_kind":
					delete(class, "kind")
				case "bad_kind":
					class["kind"] = "arbitrary"
				case "missing_running":
					delete(item["dispatch"].(map[string]any)["values"].(map[string]any), "running")
				case "missing_slots":
					delete(item["dispatch"].(map[string]any)["values"].(map[string]any), "slots_in_use")
				case "invalid_slots":
					item["dispatch"].(map[string]any)["values"].(map[string]any)["slots_in_use"] = 5
				case "missing_success":
					delete(item["collection"].(map[string]any)["values"].(map[string]any), "success")
				case "missing_enabled":
					delete(item["observation"].(map[string]any)["values"].(map[string]any), "enabled")
				case "missing_unknown_age":
					delete(class["values"].(map[string]any), "unknown_age")
				case "null_pending":
					class["values"].(map[string]any)["pending"] = nil
				case "missing_known_age":
					delete(class["values"].(map[string]any), "oldest_known_age_seconds")
				case "stale_collection":
					item["collection"].(map[string]any)["values"].(map[string]any)["last_success_at"] = now.Add(-3 * time.Minute).Format(time.RFC3339)
				case "bad_class_order":
					class["class"] = "held"
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer app_task-private-token" {
						t.Error("unprotected app_task read")
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
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "app_task-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("app_task added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.HandoffHealth == nil || report.RuntimeBuilds.BuilderHealth == nil || report.RuntimeBuilds.LoopHealth == nil || report.RuntimeBuilds.SnapshotPrimeHealth == nil || report.RuntimeBuilds.PreparationHealth == nil || report.RuntimeBuilds.PostCaptureHealth == nil || report.RuntimeBuilds.BuildRecoveryHealth == nil {
					t.Fatal("app_task changed health or other sections")
				}
				missing := strings.HasPrefix(fault, "missing_") || fault == "old_api" || fault == "wrong_scope" || fault == "wrong_job" || fault == "null_pending" || fault == "stale_collection" || fault == "bad_class_order" || fault == "bad_kind" || fault == "invalid_slots"
				if (report.RuntimeBuilds.AppTaskHealth == nil) != missing {
					t.Fatal("malformed app_task was not isolated")
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

func TestAppTaskHistoryClassTransitionsOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		base := deploymentAppTaskBaseTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		state := "empty"
		if i%2 == 1 {
			state = "nonempty"
		}
		if i == 5 {
			state = "unknown_ages"
		}
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if i != 3 {
			current.AppTaskHealth = deploymentAppTaskFixture(base, state)
		}
		out = r.observe(current, at)
	}
	p := out.Progress.AppTaskHealth
	if p.NonemptyTransitions != 39 || !p.ObservedUnknownAges || !p.ObservedIncompleteEvidence || p.EmptyThroughoutObservedPolls || !p.FirstNonemptyAt.Equal(now.Add(time.Second)) || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("bounded app_task history lost evidence")
	}
	out.History.Events[0].AppTaskHealth.Classes[0].NonemptyViews = 999
	*p.FirstNonemptyAt = now.Add(-time.Hour)
	base := deploymentAppTaskBaseTest(t, now.Add(80*time.Second), now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, AppTaskHealth: deploymentAppTaskFixture(base, "nonempty")}
	next := r.observe(current, base.GeneratedAt)
	if next.History.Events[0].AppTaskHealth.Classes[0].NonemptyViews == 999 || !next.Progress.AppTaskHealth.FirstNonemptyAt.Equal(now.Add(time.Second)) {
		t.Fatal("history aliased returned view")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.AppTaskEnvelopeOmittedItems != 1 || len(next.AppTaskHealth.Items) != 1 || fitted.RuntimeBuilds.AppTaskHealth.Counts != next.AppTaskHealth.Counts {
		t.Fatal("fitting changed aggregates or source")
	}
	// Different classes must be events; routine source clocks and ages must not.
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	for i := range 4 {
		copy := operatorobs.CloneRuntimeAppTaskHealth(current.AppTaskHealth)
		*copy.Items[0].Classes[0].Values.OldestKnownAgeSeconds += float64(i)
		current.AppTaskHealth = copy
		out = r.observe(current, base.GeneratedAt)
	}
	if out.History.TotalEvents != 1 {
		t.Fatal("routine age churn added history")
	}
	h := current.AppTaskHealth
	h.Classes[0].NonemptyViews, h.Classes[0].EmptyViews = 0, 1
	h.Classes[5].NonemptyViews, h.Classes[5].EmptyViews = 1, 0
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 2 {
		t.Fatal("class change missing from history")
	}
}

func deploymentAppTaskKeys() []string {
	var keys []string
	for _, kind := range []string{"release", "manual", "cron"} {
		for _, class := range []string{"queued", "retry_wait", "held", "deadline_overdue", "restoring", "running", "cancellation_pending", "lease_expired", "unknown"} {
			keys = append(keys, kind+"/"+class)
		}
	}
	return keys
}
