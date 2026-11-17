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

func deploymentDelayedDispatchFixture(t *testing.T, now time.Time, role, state string) (api.ObsRuntimeBuilds, *operatorobs.RuntimeDelayedTaskDispatch) {
	t.Helper()
	base, _ := deploymentOperationFixture(t, now, role, state)
	h := &operatorobs.RuntimeDelayedTaskDispatch{RuntimeDelayedTaskDispatchSummary: operatorobs.RuntimeDelayedTaskDispatchSummary{Scope: operatorobs.RuntimeAppTaskOperationsScope, State: "baseline", Reason: "comparison_not_requested"}, ComparisonAt: base.Process.ComparisonAt, Items: []operatorobs.RuntimeDelayedTaskDispatchItem{}}
	dims := []operatorobs.RuntimeDelayedTaskDispatchDimension{}
	for _, outcome := range []string{"success", "retry", "failed", "dead_letter"} {
		dims = append(dims, operatorobs.RuntimeDelayedTaskDispatchDimension{Outcome: outcome})
	}
	for _, d := range dims {
		h.Outcomes = append(h.Outcomes, operatorobs.RuntimeDelayedTaskDispatchOutcomeSummary{RuntimeDelayedTaskDispatchDimension: d})
	}
	for _, target := range base.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		item := operatorobs.RuntimeDelayedTaskDispatchItem{Job: target.Job, Node: target.Node, TargetID: target.TargetID, ScrapeObservedAt: &now, Process: target.Process, Signals: []operatorobs.RuntimeDelayedTaskDispatchSignal{}}
		h.Counts.Expected += 4
		for i, d := range dims {
			s := operatorobs.RuntimeDelayedTaskDispatchSignal{RuntimeDelayedTaskDispatchDimension: d, State: "baseline", Current: &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: now}}
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
				if state == "errors" && d.Outcome == "failed" {
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
		h.State, h.Reason = "increased", "delayed_task_dispatch_increases_observed"
	}
	if !operatorobs.RuntimeDelayedTaskDispatchValid(base, h) {
		t.Fatal("invalid Delayed-task dispatch fixture")
	}
	return base, h
}
func TestDelayedTaskDispatchTransportAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"valid", "old_api", "missing_value", "null_value", "missing_scope", "unknown_field", "missing_outcome", "wrong_outcome", "forged_delta"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base, h := deploymentDelayedDispatchFixture(t, now, role, "baseline")
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, DelayedTaskDispatch: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				switch fault {
				case "old_api":
					delete(raw, "delayed_task_dispatch")
				case "missing_scope":
					delete(raw["delayed_task_dispatch"].(map[string]any), "scope")
				case "missing_outcome":
					delete(raw["delayed_task_dispatch"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any), "outcome")
				case "wrong_outcome":
					raw["delayed_task_dispatch"].(map[string]any)["outcomes"].([]any)[0].(map[string]any)["outcome"] = "invalid"
				case "forged_delta":
					raw["delayed_task_dispatch"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["delta"] = 1
				case "missing_value", "null_value":
					p := raw["delayed_task_dispatch"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["current"].(map[string]any)
					if fault == "missing_value" {
						delete(p, "value")
					} else {
						p["value"] = nil
					}
				case "unknown_field":
					raw["delayed_task_dispatch"].(map[string]any)["private_error"] = "private-provider-error"
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
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || (report.RuntimeBuilds.DelayedTaskDispatch == nil) != missing || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.CommandCronHealth == nil {
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
func TestDelayedTaskDispatchDeltasHistoryOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		state := "unchanged"
		if i%2 == 1 {
			state = "errors"
		}
		base, h := deploymentDelayedDispatchFixture(t, at, "single-box-v2", state)
		body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, DelayedTaskDispatch: h})
		var response obsDeploymentRuntimeBuildResponse
		if json.Unmarshal(body, &response) != nil || response.DelayedTaskDispatch == nil {
			t.Fatal("valid compared evidence lost")
		}
		current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, DelayedTaskDispatch: h}
		if i == 3 {
			current.DelayedTaskDispatch = nil
		}
		out = r.observe(current, at)
	}
	p := out.Progress.DelayedTaskDispatch
	if !p.ObservedIncreases || !slices.Contains(p.ObservedOutcomes, operatorobs.RuntimeDelayedTaskDispatchDimension{Outcome: "failed"}) || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("history lost bounded outcome evidence")
	}
	out.History.Events[0].DelayedTaskDispatch.Outcomes[1].Increased = 999
	*p.FirstIncreaseAt = now.Add(-time.Hour)
	base, h := deploymentDelayedDispatchFixture(t, now.Add(81*time.Second), "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, DelayedTaskDispatch: h}
	next := r.observe(current, base.GeneratedAt)
	if next.Progress.DelayedTaskDispatch.FirstIncreaseAt.Before(now) || next.History.Events[0].DelayedTaskDispatch.Outcomes[1].Increased == 999 {
		t.Fatal("history aliased caller")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.DelayedTaskDispatchEnvelopeOmittedItems != 1 || len(next.DelayedTaskDispatch.Items) != 1 {
		t.Fatal("fit mutated source")
	}
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	current.DelayedTaskDispatch = operatorobs.CloneRuntimeDelayedTaskDispatch(h)
	s := &current.DelayedTaskDispatch.Items[0].Signals[2]
	s.Current.Value++
	*s.Delta++
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 1 {
		t.Fatal("delta magnitude churn created history")
	}
}

func TestDelayedTaskDispatchProgressKeepsCanonicalUnionAcrossGaps(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	var expected []operatorobs.RuntimeDelayedTaskDispatchDimension
	for n := range 4 {
		at := now.Add(time.Duration(n) * time.Second)
		base, h := deploymentDelayedDispatchFixture(t, at, "single-box-v2", "unchanged")
		i := 3 - n // Observation order differs from the fixed wire order.
		s := &h.Items[0].Signals[i]
		s.Current.Value++
		*s.Delta = 1
		s.State = "increased"
		h.Counts.Increased++
		h.Counts.Unchanged--
		h.Outcomes[i].Increased++
		h.Outcomes[i].Unchanged--
		h.State, h.Reason = "increased", "delayed_task_dispatch_increases_observed"
		if !operatorobs.RuntimeDelayedTaskDispatchValid(base, h) {
			t.Fatal("invalid category observation")
		}
		out = r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, DelayedTaskDispatch: h}, at)
		expected = nil
		for _, o := range h.Outcomes {
			expected = append(expected, o.RuntimeDelayedTaskDispatchDimension)
		}
	}
	out = r.observe(&obsDeploymentRuntimeBuilds{Advisory: true, State: "unknown", Reason: "source_server_error"}, now.Add(6*time.Second))
	p := out.Progress.DelayedTaskDispatch
	if !p.ObservedIncreases || !p.ObservedIncompleteEvidence || !slices.Equal(p.ObservedOutcomes, expected) || p.FirstIncreaseAt == nil || p.LastIncreaseAt == nil || !p.FirstIncreaseAt.Equal(now) || !p.LastIncreaseAt.Equal(now.Add(3*time.Second)) {
		t.Fatal("lost or reordered sticky categories")
	}
	p.ObservedOutcomes[0].Outcome = "private-invalid-copy"
	again := r.snapshot(out)
	if again.Progress.DelayedTaskDispatch.ObservedOutcomes[0].Outcome != "success" {
		t.Fatal("category union aliased caller")
	}
}

func TestDelayedTaskDispatchPreviewYieldsFirst(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base, delivery := deploymentDelayedDispatchFixture(t, now, "single-box-v2", "errors")
	_, timing := deploymentCronTimingFixture(t, now, "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, DelayedTaskDispatch: delivery, CommandCronTiming: timing}
	report := obsDeploymentReport{RuntimeBuilds: current}
	body, _ := json.MarshalIndent(report, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(report, len(body)-100)
	if fitted.RuntimeBuilds.DelayedTaskDispatchEnvelopeOmittedItems != 1 || fitted.RuntimeBuilds.CommandCronTimingEnvelopeOmittedItems != 0 || len(fitted.RuntimeBuilds.CommandCronTiming.Items) != 1 || len(current.DelayedTaskDispatch.Items) != 1 {
		t.Fatal("delayed-task dispatch preview displaced existing evidence or mutated source")
	}
}

func TestDelayedTaskDispatchSelectedView(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base, h := deploymentDelayedDispatchFixture(t, now, "single-box-v2", "baseline")
	wanted, err := operatorobs.ParseRuntimeDiagnostics(base.ExpectedRole, "delayed_task_dispatch")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := deploymentSnapshotTest(now)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer dispatch-token" || r.Method != "GET" {
			t.Error("unprotected dispatch read")
		}
		switch r.URL.Path {
		case operatorobs.RuntimeBuildsPath:
			if r.URL.Query().Get("diagnostics") != "delayed_task_dispatch" {
				t.Error("wrong selector")
			}
			_ = json.NewEncoder(w).Encode(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, Diagnostics: wanted, DelayedTaskDispatch: h})
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
	opts := obsDeploymentOptions{release: base.RequestedRelease, after: base.EvidenceAfter, expectedRole: base.ExpectedRole, runtimeDiagnostics: wanted, wait: time.Minute, poll: time.Second}
	report, code := runObsDeploymentVerification(t.Context(), opts, newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "dispatch-token"}, opts), func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("selection added poll"); return nil }, func(obsDeploymentReport) error { return nil })
	out := report.RuntimeBuilds
	if code != 0 || report.HadConfirmedFaults || calls.Load() != 3 || out.DiagnosticsSource != "server" || out.DelayedTaskDispatch == nil || out.Progress.DelayedTaskDispatch == nil || out.Progress.DelayedTaskDispatch.ObservedIncompleteEvidence || out.Progress.HTTPCronDelivery != nil {
		t.Fatal("targeted dispatch view changed core or invented gaps")
	}
}
