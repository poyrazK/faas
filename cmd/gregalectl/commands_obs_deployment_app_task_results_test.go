package main

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deploymentResultFixture(t *testing.T, now time.Time, role, state string) (api.ObsRuntimeBuilds, *operatorobs.RuntimeAppTaskResults) {
	t.Helper()
	base, _ := deploymentOperationFixture(t, now, role, state)
	h := &operatorobs.RuntimeAppTaskResults{RuntimeAppTaskResultsSummary: operatorobs.RuntimeAppTaskResultsSummary{Scope: operatorobs.RuntimeAppTaskOperationsScope, State: "baseline", Reason: "comparison_not_requested"}, ComparisonAt: base.Process.ComparisonAt, Items: []operatorobs.RuntimeAppTaskResultItem{}}
	dims := []operatorobs.RuntimeAppTaskResultDimension{}
	for _, kind := range []string{"release", "manual", "cron"} {
		for _, status := range []string{"succeeded", "failed", "timed_out", "cancelled", "queued"} {
			dims = append(dims, operatorobs.RuntimeAppTaskResultDimension{Source: "completion", Kind: kind, Outcome: status})
		}
	}
	for _, outcome := range []string{"requeued_restores", "failed_runs", "cancelled"} {
		dims = append(dims, operatorobs.RuntimeAppTaskResultDimension{Source: "recovery", Outcome: outcome})
	}
	for _, d := range dims {
		h.Outcomes = append(h.Outcomes, operatorobs.RuntimeAppTaskResultOutcome{RuntimeAppTaskResultDimension: d})
	}
	for _, target := range base.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		item := operatorobs.RuntimeAppTaskResultItem{Job: target.Job, Node: target.Node, TargetID: target.TargetID, ScrapeObservedAt: &now, Process: target.Process, Signals: []operatorobs.RuntimeAppTaskResultSignal{}}
		h.Counts.Expected += 18
		for i, d := range dims {
			s := operatorobs.RuntimeAppTaskResultSignal{RuntimeAppTaskResultDimension: d, State: "baseline", Current: &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: now}}
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
				if state == "errors" && (d.Outcome == "failed" || d.Outcome == "failed_runs") {
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
		h.State, h.Reason = "increased", "result_increases_observed"
	}
	if !operatorobs.RuntimeAppTaskResultsValid(base, h) {
		t.Fatal("invalid result fixture")
	}
	return base, h
}
func TestAppTaskResultTransportAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"valid", "old_api", "missing_value", "null_value", "missing_scope", "unknown_field", "missing_kind", "recovery_kind", "forged_delta"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base, h := deploymentResultFixture(t, now, role, "baseline")
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, AppTaskResults: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				switch fault {
				case "old_api":
					delete(raw, "app_task_results")
				case "missing_scope":
					delete(raw["app_task_results"].(map[string]any), "scope")
				case "missing_kind":
					delete(raw["app_task_results"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any), "kind")
				case "recovery_kind":
					raw["app_task_results"].(map[string]any)["outcomes"].([]any)[15].(map[string]any)["kind"] = nil
				case "forged_delta":
					raw["app_task_results"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["delta"] = 1
				case "missing_value", "null_value":
					p := raw["app_task_results"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["current"].(map[string]any)
					if fault == "missing_value" {
						delete(p, "value")
					} else {
						p["value"] = nil
					}
				case "unknown_field":
					raw["app_task_results"].(map[string]any)["private_error"] = "private-provider-error"
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
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || (report.RuntimeBuilds.AppTaskResults == nil) != missing || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.CommandCronHealth == nil {
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
func TestAppTaskResultDeltasHistoryOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		state := "unchanged"
		if i%2 == 1 {
			state = "errors"
		}
		base, h := deploymentResultFixture(t, at, "single-box-v2", state)
		body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, AppTaskResults: h})
		var response obsDeploymentRuntimeBuildResponse
		if json.Unmarshal(body, &response) != nil || response.AppTaskResults == nil {
			t.Fatal("valid compared evidence lost")
		}
		current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, AppTaskResults: h}
		if i == 3 {
			current.AppTaskResults = nil
		}
		out = r.observe(current, at)
	}
	p := out.Progress.AppTaskResults
	if !p.ObservedIncreases || !p.ObservedFailed || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("history lost bounded outcome evidence")
	}
	out.History.Events[0].AppTaskResults.Outcomes[1].Increased = 999
	*p.FirstIncreaseAt = now.Add(-time.Hour)
	base, h := deploymentResultFixture(t, now.Add(81*time.Second), "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, AppTaskResults: h}
	next := r.observe(current, base.GeneratedAt)
	if next.Progress.AppTaskResults.FirstIncreaseAt.Before(now) || next.History.Events[0].AppTaskResults.Outcomes[1].Increased == 999 {
		t.Fatal("history aliased caller")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.AppTaskResultsEnvelopeOmittedItems != 1 || len(next.AppTaskResults.Items) != 1 {
		t.Fatal("fit mutated source")
	}
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	current.AppTaskResults = operatorobs.CloneRuntimeAppTaskResults(h)
	s := &current.AppTaskResults.Items[0].Signals[1]
	s.Current.Value++
	*s.Delta++
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 1 {
		t.Fatal("delta magnitude churn created history")
	}
}
