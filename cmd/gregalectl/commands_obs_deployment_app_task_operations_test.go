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

func deploymentOperationFixture(t *testing.T, now time.Time, role, state string) (api.ObsRuntimeBuilds, *operatorobs.RuntimeAppTaskOperations) {
	t.Helper()
	base := deploymentPostCaptureBaseTest(t, now, now.Add(-time.Minute), strings.Repeat("a", 40), role)
	deploymentReadinessFixture(&base, "ready", "ready")
	at, uptime, priorUptime := now.Add(-30*time.Second), float64(130), float64(100)
	base.Process = &api.ObsRuntimeProcess{State: "baseline", Reason: "awaiting_comparison", Counts: api.ObsRuntimeProcessCounts{Fresh: len(base.Items)}}
	if state != "baseline" {
		base.Process.State, base.Process.Reason, base.Process.ComparisonAt = "unchanged", "", &at
		base.Process.Counts.Compared = len(base.Items)
		base.Process.Counts.Unchanged = len(base.Items)
	}
	for i := range base.Items {
		p := &api.ObsRuntimeProcessItem{State: "fresh", ObservedAt: &now, UptimeSeconds: &uptime, Comparison: "baseline"}
		if state != "baseline" {
			p.Comparison = "unchanged"
			p.PreviousObservedAt = &at
			p.PreviousUptimeSeconds = &priorUptime
		}
		base.Items[i].Process = p
	}
	h := &operatorobs.RuntimeAppTaskOperations{RuntimeAppTaskOperationsSummary: operatorobs.RuntimeAppTaskOperationsSummary{Scope: operatorobs.RuntimeAppTaskOperationsScope, State: "baseline", Reason: "comparison_not_requested", Incomplete: true}, ComparisonAt: base.Process.ComparisonAt, Items: []operatorobs.RuntimeAppTaskOperationItem{}}
	ops := []string{"restore", "renew", "execute", "destroy", "complete"}
	outcomes := []string{"success", "error", "canceled", "lease_lost", "empty", "cancellation_pending", "capacity"}
	for _, op := range ops {
		h.Operations = append(h.Operations, operatorobs.RuntimeAppTaskOperationSummary{Operation: op})
	}
	for _, target := range base.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		item := operatorobs.RuntimeAppTaskOperationItem{Job: target.Job, Node: target.Node, TargetID: target.TargetID, ScrapeObservedAt: &now, Process: target.Process, Signals: []operatorobs.RuntimeAppTaskOperationSignal{}}
		h.Counts.Expected += 35
		for i, op := range ops {
			for _, outcome := range outcomes {
				s := operatorobs.RuntimeAppTaskOperationSignal{Operation: op, Outcome: outcome, State: "missing", Reason: "metric_missing"}
				o := &h.Operations[i]
				if outcome == "capacity" {
					h.Counts.Missing++
					o.Unknown++
				} else {
					s.Current = &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: now}
					h.Counts.Observed++
					s.Reason = ""
					if state == "baseline" {
						s.State = "baseline"
						h.Counts.Baseline++
						o.Baseline++
					} else {
						s.Previous = &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: at}
						delta := int64(0)
						s.Delta = &delta
						s.State = "unchanged"
						h.Counts.Unchanged++
						o.Unchanged++
						if state == "errors" && op == "restore" && outcome == "error" {
							s.Current.Value = 103
							*s.Delta = 3
							s.State = "increased"
							h.Counts.Unchanged--
							o.Unchanged--
							h.Counts.Increased++
							o.Increased++
							o.ErrorIncreases++
						}
					}
				}
				item.Signals = append(item.Signals, s)
			}
		}
		h.Items = append(h.Items, item)
	}
	if state != "baseline" {
		h.State, h.Reason = "incomplete", "incomplete_operation_evidence"
	}
	if state == "errors" {
		h.State, h.Reason = "increased", "operation_increases_observed"
	}
	if !operatorobs.RuntimeBuildsValid(base) || !operatorobs.RuntimeAppTaskOperationsValid(base, h) {
		t.Fatal("invalid operation fixture")
	}
	return base, h
}
func TestAppTaskOperationTransportAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"valid", "old_api", "missing_value", "null_value", "missing_scope", "unknown_field"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base, h := deploymentOperationFixture(t, now, role, "baseline")
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, AppTaskOperations: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				switch fault {
				case "old_api":
					delete(raw, "app_task_operations")
				case "missing_scope":
					delete(raw["app_task_operations"].(map[string]any), "scope")
				case "missing_value", "null_value":
					p := raw["app_task_operations"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["current"].(map[string]any)
					if fault == "missing_value" {
						delete(p, "value")
					} else {
						p["value"] = nil
					}
				case "unknown_field":
					raw["app_task_operations"].(map[string]any)["private_error"] = "private-provider-error"
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
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || (report.RuntimeBuilds.AppTaskOperations == nil) != missing || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.CommandCronHealth == nil {
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
func TestAppTaskOperationDeltasHistoryOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		state := "unchanged"
		if i%2 == 1 {
			state = "errors"
		}
		base, h := deploymentOperationFixture(t, at, "single-box-v2", state)
		body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, AppTaskOperations: h})
		var response obsDeploymentRuntimeBuildResponse
		if json.Unmarshal(body, &response) != nil || response.AppTaskOperations == nil {
			t.Fatal("valid compared evidence lost")
		}
		current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, AppTaskOperations: h}
		if i == 3 {
			current.AppTaskOperations = nil
		}
		out = r.observe(current, at)
	}
	p := out.Progress.AppTaskOperations
	if !p.ObservedIncreases || !p.ObservedErrors || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("history lost bounded outcome evidence")
	}
	out.History.Events[0].AppTaskOperations.Operations[0].ErrorIncreases = 999
	*p.FirstIncreaseAt = now.Add(-time.Hour)
	base, h := deploymentOperationFixture(t, now.Add(81*time.Second), "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, AppTaskOperations: h}
	next := r.observe(current, base.GeneratedAt)
	if next.Progress.AppTaskOperations.FirstIncreaseAt.Before(now) || next.History.Events[0].AppTaskOperations.Operations[0].ErrorIncreases == 999 {
		t.Fatal("history aliased caller")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.AppTaskOperationsEnvelopeOmittedItems != 1 || len(next.AppTaskOperations.Items) != 1 {
		t.Fatal("fit mutated source")
	}
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	current.AppTaskOperations = operatorobs.CloneRuntimeAppTaskOperations(h)
	s := &current.AppTaskOperations.Items[0].Signals[1]
	s.Current.Value++
	*s.Delta++
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 1 {
		t.Fatal("delta magnitude churn created history")
	}
}
