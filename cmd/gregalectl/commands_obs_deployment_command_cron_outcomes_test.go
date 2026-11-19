package main

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deploymentCronOutcomeFixture(t *testing.T, now time.Time, role, state string) (api.ObsRuntimeBuilds, *operatorobs.RuntimeCommandCronOutcomes) {
	t.Helper()
	base, _ := deploymentOperationFixture(t, now, role, state)
	h := &operatorobs.RuntimeCommandCronOutcomes{RuntimeCommandCronOutcomesSummary: operatorobs.RuntimeCommandCronOutcomesSummary{Scope: operatorobs.RuntimeAppTaskOperationsScope, State: "baseline", Reason: "comparison_not_requested"}, ComparisonAt: base.Process.ComparisonAt, Items: []operatorobs.RuntimeCommandCronOutcomeItem{}}
	dims := []operatorobs.RuntimeCommandCronOutcomeDimension{}
	for _, g := range []struct {
		source   string
		outcomes []string
	}{
		{"sweep", []string{"success", "error", "canceled"}},
		{"decision", []string{"disabled", "not_due", "account_held", "plan_limit", "replacement_wait", "not_owned", "invalid_schedule", "lookup_error", "store_unavailable", "no_live_deployment", "canceled", "legacy_queued", "legacy_skipped", "legacy_stale", "legacy_error"}},
		{"admission", []string{"queued", "exclusive_pending", "coalesced", "skipped_overlap", "missed_deadline", "stale", "deployment_unavailable", "rejected", "error", "canceled", "unknown"}},
	} {
		for _, o := range g.outcomes {
			dims = append(dims, operatorobs.RuntimeCommandCronOutcomeDimension{Source: g.source, Outcome: o})
		}
	}
	for _, d := range dims {
		h.Outcomes = append(h.Outcomes, operatorobs.RuntimeCommandCronOutcomeSummary{RuntimeCommandCronOutcomeDimension: d})
	}
	for _, target := range base.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		item := operatorobs.RuntimeCommandCronOutcomeItem{Job: target.Job, Node: target.Node, TargetID: target.TargetID, ScrapeObservedAt: &now, Process: target.Process, Signals: []operatorobs.RuntimeCommandCronOutcomeSignal{}}
		h.Counts.Expected += 29
		for i, d := range dims {
			s := operatorobs.RuntimeCommandCronOutcomeSignal{RuntimeCommandCronOutcomeDimension: d, State: "baseline", Current: &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: now}}
			h.Counts.Observed++
			if state == "baseline" {
				h.Counts.Baseline++
				h.Outcomes[i].Baseline++
			} else {
				s.Previous = &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: *base.Process.ComparisonAt}
				delta := int64(0)
				s.Delta = &delta
				s.State = "unchanged"
				h.Counts.Unchanged++
				h.Outcomes[i].Unchanged++
				if state == "errors" && d.Source == "admission" && d.Outcome == "error" {
					s.Current.Value = 103
					*s.Delta = 3
					s.State = "increased"
					h.Counts.Unchanged--
					h.Outcomes[i].Unchanged--
					h.Counts.Increased++
					h.Outcomes[i].Increased++
				}
			}
			item.Signals = append(item.Signals, s)
		}
		h.Items = append(h.Items, item)
	}
	if state != "baseline" {
		h.State, h.Reason = "unchanged", ""
	}
	if state == "errors" {
		h.State, h.Reason = "increased", "cron_outcome_increases_observed"
	}
	if !operatorobs.RuntimeCommandCronOutcomesValid(base, h) {
		t.Fatal("invalid cron outcome fixture")
	}
	return base, h
}
func TestCommandCronOutcomeTransportAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"valid", "old_api", "missing_value", "null_value", "missing_scope", "unknown_field", "missing_source", "wrong_source", "forged_delta"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base, h := deploymentCronOutcomeFixture(t, now, role, "baseline")
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, CommandCronOutcomes: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				switch fault {
				case "old_api":
					delete(raw, "command_cron_outcomes")
				case "missing_scope":
					delete(raw["command_cron_outcomes"].(map[string]any), "scope")
				case "missing_source":
					delete(raw["command_cron_outcomes"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any), "source")
				case "wrong_source":
					raw["command_cron_outcomes"].(map[string]any)["outcomes"].([]any)[0].(map[string]any)["source"] = "invalid"
				case "forged_delta":
					raw["command_cron_outcomes"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["delta"] = 1
				case "missing_value", "null_value":
					p := raw["command_cron_outcomes"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["current"].(map[string]any)
					if fault == "missing_value" {
						delete(p, "value")
					} else {
						p["value"] = nil
					}
				case "unknown_field":
					raw["command_cron_outcomes"].(map[string]any)["private_error"] = "private-provider-error"
				}
				var calls atomic.Int32
				snapshot := deploymentSnapshotTest(now)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer operation-token" {
						t.Error("unprotected read")
					}
					switch r.URL.Path {
					case operatorobs.RuntimeBuildsPath:
						_ = json.NewEncoder(w).Encode(raw)
					case operatorobs.ServingHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.serving.value)
					case operatorobs.NotificationHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.notifications)
					default:
						t.Error("unexpected endpoint")
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_APID_URL", server.URL)
				opts := obsDeploymentOptions{release: base.RequestedRelease, after: base.EvidenceAfter, expectedRole: role, wait: time.Minute, poll: time.Second}
				report, code := runObsDeploymentVerification(t.Context(), opts, newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "operation-token"}, opts), func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("operation evidence added poll"); return nil }, func(obsDeploymentReport) error { return nil })
				missing := fault != "valid" && fault != "unknown_field"
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || (report.RuntimeBuilds.CommandCronOutcomes == nil) != missing || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.CommandCronHealth == nil {
					t.Fatal("operation section changed qualification or isolation")
				}
				data, _ := json.Marshal(report)
				if strings.Contains(string(data), "private-provider-error") {
					t.Fatal("raw error escaped")
				}
			})
		}
	}
}
func TestCommandCronOutcomeDeltasHistoryOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		state := "unchanged"
		if i%2 == 1 {
			state = "errors"
		}
		base, h := deploymentCronOutcomeFixture(t, at, "single-box-v2", state)
		body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, CommandCronOutcomes: h})
		var response obsDeploymentRuntimeBuildResponse
		if json.Unmarshal(body, &response) != nil || response.CommandCronOutcomes == nil {
			t.Fatal("valid compared evidence lost")
		}
		current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronOutcomes: h}
		if i == 3 {
			current.CommandCronOutcomes = nil
		}
		out = r.observe(current, at)
	}
	p := out.Progress.CommandCronOutcomes
	if !p.ObservedIncreases || !slices.Contains(p.ObservedOutcomes, operatorobs.RuntimeCommandCronOutcomeDimension{Source: "admission", Outcome: "error"}) || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("history lost bounded outcome evidence")
	}
	out.History.Events[0].CommandCronOutcomes.Outcomes[1].Increased = 999
	*p.FirstIncreaseAt = now.Add(-time.Hour)
	base, h := deploymentCronOutcomeFixture(t, now.Add(81*time.Second), "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronOutcomes: h}
	next := r.observe(current, base.GeneratedAt)
	if next.Progress.CommandCronOutcomes.FirstIncreaseAt.Before(now) || next.History.Events[0].CommandCronOutcomes.Outcomes[1].Increased == 999 {
		t.Fatal("history aliased caller")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.CommandCronOutcomesEnvelopeOmittedItems != 1 || len(next.CommandCronOutcomes.Items) != 1 {
		t.Fatal("fit mutated source")
	}
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	current.CommandCronOutcomes = operatorobs.CloneRuntimeCommandCronOutcomes(h)
	s := &current.CommandCronOutcomes.Items[0].Signals[26]
	s.Current.Value++
	*s.Delta++
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 1 {
		t.Fatal("delta magnitude churn created history")
	}
}

func TestCommandCronOutcomeProgressKeepsCanonicalUnionAcrossGaps(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	var expected []operatorobs.RuntimeCommandCronOutcomeDimension
	for n := range 29 {
		at := now.Add(time.Duration(n) * time.Second)
		base, h := deploymentCronOutcomeFixture(t, at, "single-box-v2", "unchanged")
		i := 28 - n // Observation order differs from the fixed wire order.
		s := &h.Items[0].Signals[i]
		s.Current.Value++
		*s.Delta = 1
		s.State = "increased"
		h.Counts.Increased++
		h.Counts.Unchanged--
		h.Outcomes[i].Increased++
		h.Outcomes[i].Unchanged--
		h.State, h.Reason = "increased", "cron_outcome_increases_observed"
		if !operatorobs.RuntimeCommandCronOutcomesValid(base, h) {
			t.Fatal("invalid category observation")
		}
		out = r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronOutcomes: h}, at)
		expected = nil
		for _, o := range h.Outcomes {
			expected = append(expected, o.RuntimeCommandCronOutcomeDimension)
		}
	}
	out = r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "unknown", Reason: "source_server_error"}, now.Add(30*time.Second))
	p := out.Progress.CommandCronOutcomes
	if !p.ObservedIncreases || !p.ObservedIncompleteEvidence || !slices.Equal(p.ObservedOutcomes, expected) || p.FirstIncreaseAt == nil || p.LastIncreaseAt == nil || !p.FirstIncreaseAt.Equal(now) || !p.LastIncreaseAt.Equal(now.Add(28*time.Second)) {
		t.Fatal("lost or reordered sticky categories")
	}
	p.ObservedOutcomes[0].Source = "private-invalid-copy"
	again := r.snapshot(out)
	if again.Progress.CommandCronOutcomes.ObservedOutcomes[0].Source != "sweep" {
		t.Fatal("category union aliased caller")
	}
}
