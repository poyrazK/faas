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

func deploymentPreparationFixture(e api.ObsRuntimeBuilds, state string) *operatorobs.RuntimePreparationHealth {
	now := e.GeneratedAt
	signal := operatorobs.RuntimeHandoffSignal{State: "available", ObservedAt: &now}
	item := operatorobs.RuntimePreparationItem{State: "available", ScrapeObservedAt: &now, Collection: operatorobs.RuntimeHandoffCollection{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimeHandoffCollectionValues{Success: true, LastSuccessAt: &now}}, Observation: operatorobs.RuntimePreparationObservation{RuntimeHandoffSignal: signal, Values: &operatorobs.RuntimePreparationObservationValues{Enabled: true}}}
	for _, target := range e.Items {
		if target.Job == "imaged" {
			item.TargetID, item.Node = target.TargetID, target.Node
		}
	}
	h := &operatorobs.RuntimePreparationHealth{RuntimePreparationSummary: operatorobs.RuntimePreparationSummary{State: "empty", Counts: operatorobs.RuntimePreparationCounts{Expected: 1, Observed: 1, Complete: 1, EmptyScopes: 1, EnabledScopes: 1}}, Items: []operatorobs.RuntimePreparationItem{}}
	for _, name := range []string{"preparing", "layer_published", "scan_complete"} {
		item.Phases = append(item.Phases, operatorobs.RuntimePreparationPhase{Phase: name, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "empty", ObservedAt: &now}, Values: &operatorobs.RuntimePreparationPhaseValues{}})
		h.Phases = append(h.Phases, operatorobs.RuntimePreparationPhaseCounts{Phase: name, EmptyScopes: 1})
	}
	if state == "waiting" || state == "unknown_ages" || state == "partial" || state == "disabled" {
		h.State, h.Reason = "waiting", "pending_preparations_observed"
		h.Counts.WaitingScopes, h.Counts.EmptyScopes = 1, 0
		h.Phases[0].WaitingScopes, h.Phases[0].EmptyScopes = 1, 0
		oldest, age := now.Add(-time.Hour), float64(3600)
		item.Phases[0].State = "waiting"
		item.Phases[0].Values = &operatorobs.RuntimePreparationPhaseValues{Pending: 3, OldestPhaseStartedAt: &oldest, OldestKnownAgeSeconds: &age}
		if state == "unknown_ages" {
			item.Phases[0].Values = &operatorobs.RuntimePreparationPhaseValues{Pending: 3, UnknownAge: 3}
			h.Counts.UnknownAgeScopes, h.Phases[0].UnknownAgeScopes = 1, 1
		}
	}
	switch state {
	case "partial":
		h.Incomplete, h.Counts.Complete, h.Counts.Missing = true, 0, 1
		item.State = "missing"
		item.Phases[2] = operatorobs.RuntimePreparationPhase{Phase: "scan_complete", RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "missing", Reason: "metric_missing"}}
		h.Phases[2].EmptyScopes = 0
	case "disabled":
		item.Observation.Values.Enabled = false
		h.Counts.EnabledScopes, h.Counts.DisabledScopes = 0, 1
	case "collection_failed":
		h.State, h.Reason, h.Incomplete = "incomplete", "incomplete_preparation_evidence", true
		h.Counts.Complete, h.Counts.EmptyScopes, h.Counts.Unavailable, h.Counts.UnsuccessfulCollections = 0, 0, 1, 1
		item.State, item.Collection.Values.Success = "unavailable", false
		for i := range item.Phases {
			item.Phases[i] = operatorobs.RuntimePreparationPhase{Phase: h.Phases[i].Phase, RuntimeHandoffSignal: operatorobs.RuntimeHandoffSignal{State: "unavailable", Reason: "collection_failed"}}
			h.Phases[i].EmptyScopes = 0
		}
	}
	h.Items = append(h.Items, item)
	return h
}

func TestPreparationEvidenceTransportAndMalformedIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after, release := now.Add(-time.Minute), strings.Repeat("a", 40)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"empty", "waiting", "unknown_ages", "partial", "disabled", "collection_failed", "old_api", "missing_success", "missing_enabled", "missing_unknown_age", "null_pending", "missing_known_age", "stale_collection", "bad_phase_order", "unknown_fields"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				state := fault
				if fault == "missing_known_age" {
					state = "waiting"
				}
				base := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
				deploymentReadinessFixture(&base, "ready", "ready")
				h := deploymentPreparationFixture(base, state)
				if !operatorobs.RuntimePreparationHealthValid(base, h) {
					t.Fatal("invalid preparation fixture")
				}
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, LoopHealth: deploymentLoopFixture(base, "no_reported_stall"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), HandoffHealth: deploymentHandoffFixture(base, "empty"), PreparationHealth: h})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				item := raw["preparation_health"].(map[string]any)["items"].([]any)[0].(map[string]any)
				phase := item["phases"].([]any)[0].(map[string]any)
				switch fault {
				case "old_api":
					delete(raw, "preparation_health")
				case "missing_success":
					delete(item["collection"].(map[string]any)["values"].(map[string]any), "success")
				case "missing_enabled":
					delete(item["observation"].(map[string]any)["values"].(map[string]any), "enabled")
				case "missing_unknown_age":
					delete(phase["values"].(map[string]any), "unknown_age")
				case "null_pending":
					phase["values"].(map[string]any)["pending"] = nil
				case "missing_known_age":
					delete(phase["values"].(map[string]any), "oldest_known_age_seconds")
				case "stale_collection":
					item["collection"].(map[string]any)["values"].(map[string]any)["last_success_at"] = now.Add(-3 * time.Minute).Format(time.RFC3339)
				case "bad_phase_order":
					phase["phase"] = "scan_complete"
				case "unknown_fields":
					item["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer preparation-private-token" {
						t.Error("unprotected preparation read")
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
				fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "preparation-private-token"}, opts)
				report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("preparation added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || report.RuntimeBuilds.HandoffHealth == nil || report.RuntimeBuilds.BuilderHealth == nil || report.RuntimeBuilds.LoopHealth == nil {
					t.Fatal("preparation changed health or other sections")
				}
				missing := strings.HasPrefix(fault, "missing_") || fault == "old_api" || fault == "null_pending" || fault == "stale_collection" || fault == "bad_phase_order"
				if (report.RuntimeBuilds.PreparationHealth == nil) != missing {
					t.Fatal("malformed preparation was not isolated")
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

func TestPreparationHistoryPhaseTransitionsOwnershipAndFit(t *testing.T) {
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
		if i == 5 {
			state = "unknown_ages"
		}
		current := &obsDeploymentRuntimeBuilds{pipelineSelected: true, Advisory: true, State: "matching", Evidence: &base}
		if i != 3 {
			current.PreparationHealth = deploymentPreparationFixture(base, state)
		}
		out = r.observe(current, at)
	}
	p := out.Progress.PreparationHealth
	if p.WaitingTransitions != 39 || !p.ObservedUnknownAges || !p.ObservedIncompleteEvidence || p.EmptyThroughoutObservedPolls || !p.FirstWaitingAt.Equal(now.Add(time.Second)) || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("bounded preparation history lost evidence")
	}
	out.History.Events[0].PreparationHealth.Phases[0].WaitingScopes = 999
	*p.FirstWaitingAt = now.Add(-time.Hour)
	base := deploymentRuntimeBuildProfileTest(t, now.Add(80*time.Second), now.Add(-time.Minute), strings.Repeat("a", 40), "single-box-v2")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, PreparationHealth: deploymentPreparationFixture(base, "waiting")}
	next := r.observe(current, base.GeneratedAt)
	if next.History.Events[0].PreparationHealth.Phases[0].WaitingScopes == 999 || !next.Progress.PreparationHealth.FirstWaitingAt.Equal(now.Add(time.Second)) {
		t.Fatal("history aliased returned view")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.PreparationEnvelopeOmittedItems != 1 || len(next.PreparationHealth.Items) != 1 || fitted.RuntimeBuilds.PreparationHealth.Counts != next.PreparationHealth.Counts {
		t.Fatal("fitting changed aggregates or source")
	}
	// Different phases must be events; routine source clocks and ages must not.
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	for i := range 4 {
		copy := operatorobs.CloneRuntimePreparationHealth(current.PreparationHealth)
		*copy.Items[0].Phases[0].Values.OldestKnownAgeSeconds += float64(i)
		current.PreparationHealth = copy
		out = r.observe(current, base.GeneratedAt)
	}
	if out.History.TotalEvents != 1 {
		t.Fatal("routine age churn added history")
	}
	h := current.PreparationHealth
	h.Phases[0].WaitingScopes, h.Phases[0].EmptyScopes = 0, 1
	h.Phases[1].WaitingScopes, h.Phases[1].EmptyScopes = 1, 0
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 2 {
		t.Fatal("phase change missing from history")
	}
}
