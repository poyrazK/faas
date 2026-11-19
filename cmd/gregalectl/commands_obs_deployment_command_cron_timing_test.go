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

func deploymentCronTimingFixture(t *testing.T, now time.Time, role, state string) (api.ObsRuntimeBuilds, *operatorobs.RuntimeCommandCronTiming) {
	t.Helper()
	base, _ := deploymentOperationFixture(t, now, role, state)
	h := &operatorobs.RuntimeCommandCronTiming{RuntimeCommandCronTimingSummary: operatorobs.RuntimeCommandCronTimingSummary{Scope: operatorobs.RuntimeAppTaskOperationsScope, State: "baseline", Reason: "comparison_not_requested"}, ComparisonAt: base.Process.ComparisonAt, Items: []operatorobs.RuntimeCommandCronTimingItem{}}
	dims := []operatorobs.RuntimeCommandCronTimingDimension{}
	for _, name := range []string{"le_1_seconds", "le_5_seconds", "le_15_seconds", "le_30_seconds", "le_60_seconds", "le_120_seconds", "le_300_seconds", "le_600_seconds", "le_1800_seconds", "le_3600_seconds", "le_inf", "timed", "unknown_timestamps"} {
		dims = append(dims, operatorobs.RuntimeCommandCronTimingDimension{Signal: name})
	}
	for _, d := range dims {
		h.Signals = append(h.Signals, operatorobs.RuntimeCommandCronTimingSignalSummary{RuntimeCommandCronTimingDimension: d})
	}
	for _, target := range base.Items {
		if target.Job != "schedd" && target.Job != "schedd-compute" {
			continue
		}
		item := operatorobs.RuntimeCommandCronTimingItem{Job: target.Job, Node: target.Node, TargetID: target.TargetID, ScrapeObservedAt: &now, Process: target.Process, Signals: []operatorobs.RuntimeCommandCronTimingSignal{}}
		h.Counts.Expected += 13
		for i, d := range dims {
			s := operatorobs.RuntimeCommandCronTimingSignal{RuntimeCommandCronTimingDimension: d, State: "baseline", Current: &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: now}}
			h.Counts.Observed++
			if state == "baseline" {
				h.Counts.Baseline++
				h.Signals[i].Baseline++
			} else {
				s.Previous = &operatorobs.RuntimeAppTaskOperationPoint{Value: 100, ObservedAt: *base.Process.ComparisonAt}
				delta := int64(0)
				s.Delta = &delta
				s.State = "unchanged"
				h.Counts.Unchanged++
				h.Signals[i].Unchanged++
				if state == "errors" && d.Signal == "unknown_timestamps" {
					s.Current.Value = 103
					*s.Delta = 3
					s.State = "increased"
					h.Counts.Unchanged--
					h.Signals[i].Unchanged--
					h.Counts.Increased++
					h.Signals[i].Increased++
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
		h.State, h.Reason = "increased", "cron_timing_increases_observed"
	}
	if !operatorobs.RuntimeCommandCronTimingValid(base, h) {
		t.Fatal("invalid cron timing fixture")
	}
	return base, h
}
func TestCommandCronTimingTransportAndIsolation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, fault := range []string{"valid", "old_api", "missing_value", "null_value", "missing_scope", "unknown_field", "missing_source", "wrong_source", "forged_delta"} {
			t.Run(role+"/"+fault, func(t *testing.T) {
				base, h := deploymentCronTimingFixture(t, now, role, "baseline")
				body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, CommandCronTiming: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
				var raw map[string]any
				_ = json.Unmarshal(body, &raw)
				switch fault {
				case "old_api":
					delete(raw, "command_cron_timing")
				case "missing_scope":
					delete(raw["command_cron_timing"].(map[string]any), "scope")
				case "missing_source":
					delete(raw["command_cron_timing"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any), "signal")
				case "wrong_source":
					raw["command_cron_timing"].(map[string]any)["signals"].([]any)[0].(map[string]any)["signal"] = "invalid"
				case "forged_delta":
					raw["command_cron_timing"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["delta"] = 1
				case "missing_value", "null_value":
					p := raw["command_cron_timing"].(map[string]any)["items"].([]any)[0].(map[string]any)["signals"].([]any)[0].(map[string]any)["current"].(map[string]any)
					if fault == "missing_value" {
						delete(p, "value")
					} else {
						p["value"] = nil
					}
				case "unknown_field":
					raw["command_cron_timing"].(map[string]any)["private_error"] = "private-provider-error"
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
				if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != 3 || (report.RuntimeBuilds.CommandCronTiming == nil) != missing || report.RuntimeBuilds.AppTaskHealth == nil || report.RuntimeBuilds.CommandCronHealth == nil {
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
func TestCommandCronTimingDeltasHistoryOwnershipAndFit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := newObsDeploymentBuildRecorder()
	var out *obsDeploymentRuntimeBuilds
	for i := range 80 {
		at := now.Add(time.Duration(i) * time.Second)
		state := "unchanged"
		if i%2 == 1 {
			state = "errors"
		}
		base, h := deploymentCronTimingFixture(t, at, "single-box-v2", state)
		body, _ := json.Marshal(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, CommandCronTiming: h})
		var response obsDeploymentRuntimeBuildResponse
		if json.Unmarshal(body, &response) != nil || response.CommandCronTiming == nil {
			t.Fatal("valid compared evidence lost")
		}
		current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronTiming: h}
		if i == 3 {
			current.CommandCronTiming = nil
		}
		out = r.observe(current, at)
	}
	p := out.Progress.CommandCronTiming
	if !p.ObservedIncreases || !slices.Contains(p.ObservedSignals, operatorobs.RuntimeCommandCronTimingDimension{Signal: "unknown_timestamps"}) || !p.ObservedIncompleteEvidence || out.History.DroppedEvents == 0 || out.History.RetainedBytes > api.ObsPostDeployBuildHistoryBytesMax {
		t.Fatal("history lost bounded outcome evidence")
	}
	out.History.Events[0].CommandCronTiming.Signals[1].Increased = 999
	*p.FirstIncreaseAt = now.Add(-time.Hour)
	base, h := deploymentCronTimingFixture(t, now.Add(81*time.Second), "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronTiming: h}
	next := r.observe(current, base.GeneratedAt)
	if next.Progress.CommandCronTiming.FirstIncreaseAt.Before(now) || next.History.Events[0].CommandCronTiming.Signals[1].Increased == 999 {
		t.Fatal("history aliased caller")
	}
	body, _ := json.MarshalIndent(obsDeploymentReport{RuntimeBuilds: next}, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(obsDeploymentReport{RuntimeBuilds: next}, len(body)-100)
	if fitted.RuntimeBuilds.CommandCronTimingEnvelopeOmittedItems != 1 || len(next.CommandCronTiming.Items) != 1 {
		t.Fatal("fit mutated source")
	}
	r = newObsDeploymentBuildRecorder()
	r.observe(current, base.GeneratedAt)
	current.CommandCronTiming = operatorobs.CloneRuntimeCommandCronTiming(h)
	s := &current.CommandCronTiming.Items[0].Signals[12]
	s.Current.Value++
	*s.Delta++
	out = r.observe(current, base.GeneratedAt)
	if out.History.TotalEvents != 1 {
		t.Fatal("delta magnitude churn created history")
	}
}

func TestCommandCronTimingPreviewYieldsFirst(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base, timing := deploymentCronTimingFixture(t, now, "single-box-v2", "errors")
	_, outcomes := deploymentCronOutcomeFixture(t, now, "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, CommandCronTiming: timing, CommandCronOutcomes: outcomes}
	report := obsDeploymentReport{RuntimeBuilds: current}
	body, _ := json.MarshalIndent(report, "", "  ")
	fitted := fitObsDeploymentRuntimeBuildsWithin(report, len(body)-100)
	if fitted.RuntimeBuilds.CommandCronTimingEnvelopeOmittedItems != 1 || fitted.RuntimeBuilds.CommandCronOutcomesEnvelopeOmittedItems != 0 || len(fitted.RuntimeBuilds.CommandCronOutcomes.Items) != 1 || len(current.CommandCronTiming.Items) != 1 {
		t.Fatal("timing preview displaced existing evidence or mutated source")
	}
}
