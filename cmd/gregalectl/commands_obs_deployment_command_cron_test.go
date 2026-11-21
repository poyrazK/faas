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

func deploymentCommandCronBaseTest(t *testing.T, now, after time.Time, release, role string) api.ObsRuntimeBuilds {
	t.Helper()
	base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
	for i := range base.Items {
		base.Items[i].TargetID = fmt.Sprintf("%064x", i+1)
	}
	return base
}

func deploymentCommandCronFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimeCommandCronHealth {
	now := e.GeneratedAt
	signal := operatorobs.RuntimeHandoffSignal{State: "available", ObservedAt: &now}
	started, success := now.Add(-20*time.Second), now.Add(-10*time.Second)
	item := operatorobs.RuntimeCommandCronItem{Sweep: operatorobs.RuntimeCommandCronSweep{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeCommandCronSweepValues{Running: false, LastStartedAt: &started, LastSuccessAt: &success}}, State: "available", ScrapeObservedAt: &now, Collection: operatorobs.RuntimeHandoffCollection{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeHandoffCollectionValues{Success: true, LastSuccessAt: &now}}, Observation: operatorobs.RuntimeCommandCronObservation{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeCommandCronObservationValues{Enabled: true}}}
	h := &operatorobs.RuntimeCommandCronHealth{RuntimeCommandCronSummary: operatorobs.RuntimeCommandCronSummary{Scope: operatorobs.RuntimeCommandCronScope, State: "empty", Counts: operatorobs.RuntimeCommandCronCounts{Expected: 1, Observed: 1, Complete: 1, EmptyScopes: 1, EnabledScopes: 1, SweepIdleScopes: 1}}, Items: []operatorobs.RuntimeCommandCronItem{}}
	for _, name := range []string{"due", "overlap_due", "replacement_wait", "deployment_missing", "exclusive_due", "disabled", "suspended", "account_held", "not_due", "unknown"} {
		item.Classes = append(item.Classes, operatorobs.RuntimeCommandCronClass{Class: name, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimeCommandCronClassValues{}})
		h.Classes = append(h.Classes, operatorobs.RuntimeCommandCronClassCounts{Class: name, EmptyScopes: 1})
	}
	if state == "nonempty" || state == "unknown_ages" || state == "partial" || state == "observer_disabled" || state == "due_with_hold" {
		h.State, h.Reason = "nonempty", "command_cron_evidence_observed"
		h.Counts.NonemptyScopes, h.Counts.EmptyScopes, h.Counts.DueScopes = 1, 0, 1
		h.Classes[0].NonemptyScopes, h.Classes[0].EmptyScopes = 1, 0
		oldest, age := now.Add(-time.Hour), float64(3600)
		item.Classes[0].State = "nonempty"
		item.Classes[0].Values = &operatorobs.RuntimeCommandCronClassValues{Schedules: 3, OldestDueAt: &oldest, OldestKnownDueAgeSeconds: &age}
		if state == "unknown_ages" {
			item.Classes[0].Values = &operatorobs.RuntimeCommandCronClassValues{Schedules: 3, UnknownAge: 3}
			h.Counts.UnknownAgeScopes, h.Classes[0].UnknownAgeScopes = 1, 1
		}
	}
	if state == "due_with_hold" {
		item.Classes[2].State, item.Classes[2].Values = item.Classes[0].State, item.Classes[0].Values
		item.Classes[0].State, item.Classes[0].Values = "empty", &operatorobs.RuntimeCommandCronClassValues{}
		h.Classes[2].NonemptyScopes, h.Classes[2].EmptyScopes = 1, 0
		h.Classes[0].NonemptyScopes, h.Classes[0].EmptyScopes = 0, 1
	}
	switch state {
	case "partial":
		h.Incomplete, h.Counts.Complete, h.Counts.Missing = true, 0, 1
		item.State = "missing"
		item.Classes[2] = operatorobs.RuntimeCommandCronClass{Class: "replacement_wait", RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "missing", Reason: "metric_missing"}}
		h.Classes[2].EmptyScopes = 0
	case "observer_disabled":
		item.Observation.Values.Enabled = false
		h.Counts.EnabledScopes, h.Counts.DisabledScopes = 0, 1
	case "collection_failed":
		h.State, h.Reason, h.Incomplete = "incomplete", "incomplete_command_cron_evidence", true
		h.Counts.Complete, h.Counts.EmptyScopes, h.Counts.Unavailable, h.Counts.UnsuccessfulCollections = 0, 0, 1, 1
		item.State, item.Collection.Values.Success = "unavailable", false
		for i := range item.Classes {
			item.Classes[i] = operatorobs.RuntimeCommandCronClass{Class: h.Classes[i].Class, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "collection_failed"}}
			h.Classes[i].EmptyScopes = 0
		}
	}
	for _, target := range e.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		body, _ := json.Marshal(item)
		var copy operatorobs.RuntimeCommandCronItem
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
		h.Classes[i].NonemptyScopes *= len(h.Items)
		h.Classes[i].EmptyScopes *= len(h.Items)
		h.Classes[i].UnknownAgeScopes *= len(h.Items)
	}
	return h
}

func TestCommandCronEvidenceTransportAndMalformedIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"empty", "nonempty", "due_with_hold", "unknown_ages", "partial", "observer_disabled", "collection_failed", "old_api", "missing_scope", "wrong_scope", "missing_success", "missing_enabled", "missing_unknown_age", "missing_job", "wrong_job", "null_schedules", "missing_known_age", "missing_running", "invalid_sweep_clock", "stale_collection", "bad_class_order", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				state := fault
				if fault == "missing_known_age" {
					state = "nonempty"
				}
				base := deploymentCommandCronBaseTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				h := deploymentCommandCronFixture(base, state)
				if !operatorobs.RuntimeCommandCronHealthValid(base, h) {
					t.Fatal("invalid command_cron fixture")
				}
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), HandoffHealth: deploymentHandoffFixture(base, "empty"), CommandCronHealth: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), PostCaptureHealth: deploymentPostCaptureFixture(base, "empty"), BuildRecoveryHealth: deploymentBuildRecoveryFixture(base, "empty"), SnapshotPrimeHealth: deploymentSnapshotPrimeFixture(base, "empty"), PreparationHealth: deploymentPreparationFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				item := raw["command_cron_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				class := item["classes"].([]any)[0].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "command_cron_health")
				case "missing_scope":
					delete(raw["command_cron_health"].(map[string]any), "scope")
				case "wrong_scope":
					raw["command_cron_health"].(map[string]any)["scope"] = "unique_deployments"
				case "missing_job":
					delete(item, "job")
				case "wrong_job":
					item["job"] = "imaged"
				case "missing_running":
					delete(item["sweep"].(map[string]any)["values"].(map[string]any), "running")
				case "invalid_sweep_clock":
					item["sweep"].(map[string]any)["values"].(map[string]any)["last_started_at"] = "bad-clock"
				case "missing_success":
					delete(item["collection"].(map[string]any)["values"].(map[string]any), "success")
				case "missing_enabled":
					delete(item["observation"].(map[string]any)["values"].(map[string]any), "enabled")
				case "missing_unknown_age":
					delete(class["values"].(map[string]any), "unknown_age")
				case "null_schedules":
					class["values"].(map[string]any)["schedules"] = nil
				case "missing_known_age":
					delete(class["values"].(map[string]any), "oldest_known_due_age_seconds")
				case "stale_collection":
					item["collection"].(map[string]any)["values"].(map[string]any)["last_success_at"] = now.Add(-3 * time.Minute).Format(time.RFC3339)
				case "bad_class_order":
					class["class"] = "replacement_wait"
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer command_cron-private-token" {
						t.Error("unprotected command_cron read")
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
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "command_cron-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("command_cron added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.HandoffHealth == nil || report.RuntimeBuilds.BuilderHealth == nil || report.RuntimeBuilds.LoopHealth == nil || report.RuntimeBuilds.SnapshotPrimeHealth == nil || report.RuntimeBuilds.PreparationHealth == nil || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.PostCaptureHealth == nil || report.RuntimeBuilds.BuildRecoveryHealth == nil {
					t.Fatal("command_cron changed health or other sections")
				}
				missing := strings.HasPrefix(fault, "missing_") || fault == "old_api" || fault == "wrong_scope" || fault == "wrong_job" || fault == "null_schedules" || fault == "stale_collection" || fault == "bad_class_order" || fault == "invalid_sweep_clock"
				if (report.RuntimeBuilds.CommandCronHealth == nil) != missing {
					t.Fatal("malformed command_cron was not isolated")
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

func TestCommandCronHistoryClassTransitionsOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		base := deploymentCommandCronBaseTest(t, at, now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
		state := "empty"
		if i%2 == 1 {
			state = "nonempty"
		}
		if i == 5 {
			state = "unknown_ages"
		}
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if i != 3 {
			current.CommandCronHealth = deploymentCommandCronFixture(base, state)
		}
		out = r.observe(current, at)
	}
	p := out.Progress.CommandCronHealth
	if p.NonemptyTransitions != 39 || !p.ObservedUnknownAges || !p.ObservedIncompleteEvidence || p.EmptyThroughoutObservedPolls || !p.FirstNonemptyAt.Equal(now.Add(time.Second)) || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("bounded command_cron history lost evidence")
	}
	out.History.Events[0].CommandCronHealth.Classes[0].NonemptyScopes = 999
	*p.FirstNonemptyAt = now.Add(-time.Hour)
	base := deploymentCommandCronBaseTest(t, now.Add(80*time.Second), now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronHealth: deploymentCommandCronFixture(base, "nonempty")}
	next := r.observe(current, base.GeneratedAt)
	if next.History.Events[0].CommandCronHealth.Classes[0].NonemptyScopes == 999 || !next.Progress.CommandCronHealth.FirstNonemptyAt.Equal(now.Add(time.Second)) {
		t.Fatal("history aliased returned view")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.CommandCronEnvelopeOmittedItems != 1 || len(next.CommandCronHealth.Items) != 1 || fitted.RuntimeBuilds.CommandCronHealth.Counts != next.CommandCronHealth.Counts {
		t.Fatal("fitting changed aggregates or source")
	}
	// Different classes must be events; routine source clocks and ages must not.
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	for i := range 4 {
		copy := operatorobs.CloneRuntimeCommandCronHealth(current.CommandCronHealth)
		copy.Items[0].Classes[0].Values.Schedules += int64(i)
		current.CommandCronHealth = copy
		out = r.observe(current, base.GeneratedAt)
	}
	if out.History.TotalEvents != 1 {
		t.Fatal("routine age churn added history")
	}
	h := current.CommandCronHealth
	h.Classes[0].NonemptyScopes, h.Classes[0].EmptyScopes = 0, 1
	h.Classes[1].NonemptyScopes, h.Classes[1].EmptyScopes = 1, 0
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 2 {
		t.Fatal("class change missing from history")
	}
}
