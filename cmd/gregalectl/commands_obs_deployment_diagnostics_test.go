package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeDiagnosticsCLIParsing(t *testing.T) {
	old := osStderr
	osStderr = &bytes.Buffer{}
	defer func() { osStderr = old }()
	base := []string{"--release", strings.Repeat("a", 40), "--after", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), "--admin-token", "diagnostic-token"}
	for _, raw := range []string{"all", "none", "http_cron_delivery", "preparation_health,builder_health"} {
		args := append(append([]string{}, base...), "--expected-role", "single-box-v2", "--runtime-diagnostics", raw)
		opts, _, ok := parseObsDeploymentOptions(args)
		if !ok || !operatorobs.RuntimeDiagnosticsValid(opts.expectedRole, opts.runtimeDiagnostics) {
			t.Fatal("valid CLI selector rejected")
		}
	}
	for _, raw := range []string{"", "unknown", "loop_health", "all,none", "builder_health,builder_health"} {
		args := append(append([]string{}, base...), "--expected-role", "single-box-v2", "--runtime-diagnostics", raw)
		if _, _, ok := parseObsDeploymentOptions(args); ok {
			t.Fatal("bad CLI selector accepted")
		}
	}
	if _, _, ok := parseObsDeploymentOptions(append(base, "--runtime-diagnostics", "none")); ok {
		t.Fatal("selector accepted without role")
	}
	for _, raw := range []string{"all", "none"} {
		opts, _, ok := parseObsDeploymentOptions(append(append([]string{}, base...), "--expected-role", "single-box-v1", "--runtime-diagnostics", raw))
		if !ok || len(opts.runtimeDiagnostics.Selected) != 0 || len(opts.runtimeDiagnostics.NotSelected) != 0 {
			t.Fatal("legacy selection invented advisory jobs")
		}
	}
	if _, _, ok := parseObsDeploymentOptions(append(append([]string{}, base...), "--expected-role", "single-box-v1", "--runtime-diagnostics", "builder_health")); ok {
		t.Fatal("legacy profile accepted advisory group")
	}
	opts, _, ok := parseObsDeploymentOptions(append(base, "--expected-role", "single-box-v2"))
	if !ok || opts.runtimeDiagnostics != nil {
		t.Fatal("default changed")
	}
}
func TestRuntimeDiagnosticsTransportAndFallback(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"server", "legacy", "unacknowledged", "invalid", "none"} {
		t.Run(mode, func(t *testing.T) {
			base, h := deploymentHTTPDeliveryFixture(t, now, "single-box-v2", "baseline")
			raw := "http_cron_delivery"
			if mode == "none" {
				raw = "none"
			}
			wanted, _ := operatorobs.ParseRuntimeDiagnostics(base.ExpectedRole, raw)
			var calls, selectionCalls atomic.Int32
			snapshot := deploymentSnapshotTest(now)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer diagnostic-token" {
					t.Error("unprotected or mutating read")
				}
				switch r.URL.Path {
				case operatorobs.RuntimeBuildsPath:
					var d *operatorobs.RuntimeDiagnostics
					if r.URL.Query().Has("diagnostics") {
						selectionCalls.Add(1)
						if mode == "legacy" {
							http.Error(w, "private-provider-error", 400)
							return
						}
						if mode != "unacknowledged" {
							d = operatorobs.CloneRuntimeDiagnostics(wanted)
						}
						if mode == "invalid" {
							d.Selected = []string{"private-invalid"}
						}
					}
					_ = json.NewEncoder(w).Encode(operatorobs.RuntimeBuildsLoopResponse{ObsRuntimeBuilds: base, Diagnostics: d, HTTPCronDelivery: h, AppTaskHealth: deploymentAppTaskFixture(base, "empty"), BuilderHealth: deploymentBuilderFixture(base, "waiting"), CommandCronHealth: deploymentCommandCronFixture(base, "empty")})
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
			opts := obsDeploymentOptions{release: base.RequestedRelease, after: base.EvidenceAfter, expectedRole: base.ExpectedRole, wait: time.Minute, poll: time.Second, runtimeDiagnostics: wanted}
			fetch := newObsDeploymentProcessFetcher(server.Client(), obsCredential{bearer: "diagnostic-token"}, opts)
			report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("selection added poll"); return nil }, func(obsDeploymentReport) error { return nil })
			wantSource := "server"
			wantCalls := int32(3)
			if mode == "legacy" {
				wantCalls = 4
			}
			if mode == "legacy" || mode == "invalid" || mode == "unacknowledged" {
				wantSource = "client"
			}
			out := report.RuntimeBuilds
			if code != 0 || report.State != "passed" || report.HadConfirmedFaults || calls.Load() != wantCalls || out.DiagnosticsSource != wantSource || !operatorobs.RuntimeDiagnosticsEqual(out.Diagnostics, wanted) || out.Evidence == nil || out.BuilderHealth != nil || out.AppTaskHealth != nil || out.CommandCronHealth != nil || out.Progress.BuilderHealth != nil {
				t.Fatalf("selection changed core qualification or intent: %+v", out.Diagnostics)
			}
			if mode == "none" {
				if out.HTTPCronDelivery != nil || out.Progress.HTTPCronDelivery != nil {
					t.Fatal("unselected data or false gap retained")
				}
			} else if out.HTTPCronDelivery == nil || out.Progress.HTTPCronDelivery == nil || out.Progress.HTTPCronDelivery.ObservedIncompleteEvidence {
				t.Fatal("selected baseline lost")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-") {
				t.Fatal("private diagnostic metadata leaked")
			}
			if mode == "legacy" {
				fetch(t.Context())
				if selectionCalls.Load() != 1 || calls.Load() != 7 {
					t.Fatal("legacy fallback retried every poll")
				}
			}
		})
	}
}
func TestRuntimeDiagnosticsHistoryIntentAndContinuity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	none, _ := operatorobs.ParseRuntimeDiagnostics("single-box-v2", "none")
	httpOnly, _ := operatorobs.ParseRuntimeDiagnostics("single-box-v2", "http_cron_delivery")
	r := newObsDeploymentBuildRecorder()
	base, h := deploymentHTTPDeliveryFixture(t, now, "single-box-v2", "errors")
	current := &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, HTTPCronDelivery: h, Diagnostics: httpOnly, DiagnosticsSource: "server"}
	out := r.observe(current, now)
	if !out.Progress.HTTPCronDelivery.ObservedIncreases || out.Progress.HTTPCronDelivery.ObservedIncompleteEvidence {
		t.Fatal("initial selected evidence lost")
	}
	current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, Diagnostics: none, DiagnosticsSource: "server"}
	out = r.observe(current, now.Add(time.Second))
	if !out.Progress.HTTPCronDelivery.ObservedIncreases || out.Progress.HTTPCronDelivery.ObservedIncompleteEvidence || out.Progress.BuilderHealth != nil {
		t.Fatal("intentional omission became source gap")
	}
	last := out.History.Events[len(out.History.Events)-1]
	if !operatorobs.RuntimeDiagnosticsEqual(last.Diagnostics, none) || last.HTTPCronDelivery != nil {
		t.Fatal("selection absent from history")
	}
	out.Diagnostics.Selected = append(out.Diagnostics.Selected, "invalid")
	if !operatorobs.RuntimeDiagnosticsValid(base.ExpectedRole, r.snapshot(current).Diagnostics) {
		t.Fatal("selection aliased caller")
	}
	// Starting with an intentional omission is not a source gap on first selection.
	r = newObsDeploymentBuildRecorder()
	r.observe(current, now)
	current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, HTTPCronDelivery: h, Diagnostics: httpOnly, DiagnosticsSource: "server"}
	out = r.observe(current, now.Add(time.Second))
	if out.Progress.HTTPCronDelivery.ObservedIncompleteEvidence {
		t.Fatal("first selected observation invented gap")
	}
	// Deselecting must interrupt a sampled empty-to-waiting transition.
	handoff, _ := operatorobs.ParseRuntimeDiagnostics(base.ExpectedRole, "handoff_health")
	r = newObsDeploymentBuildRecorder()
	current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, HandoffHealth: deploymentHandoffFixture(base, "empty"), Diagnostics: handoff, DiagnosticsSource: "server"}
	r.observe(current, now)
	current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, Diagnostics: none, DiagnosticsSource: "server"}
	r.observe(current, now.Add(time.Second))
	current = &obsDeploymentRuntimeBuilds{Advisory: true, State: "matching", Evidence: &base, HandoffHealth: deploymentHandoffFixture(base, "waiting"), Diagnostics: handoff, DiagnosticsSource: "server"}
	out = r.observe(current, now.Add(2*time.Second))
	if out.Progress.HandoffHealth.WaitingTransitions != 0 || out.Progress.HandoffHealth.ObservedIncompleteEvidence || out.Progress.HandoffHealth.EmptyThroughoutObservedPolls {
		t.Fatal("deselection became transition or unavailable evidence")
	}
}

func TestRuntimeDiagnosticsCombinedHistoryTransport(t *testing.T) {
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		t.Run(role, func(t *testing.T) {
			first := time.Now().UTC().Add(-100 * time.Second).Truncate(time.Second)
			r := newObsDeploymentBuildRecorder()
			var out *obsDeploymentRuntimeBuilds
			for i := range 80 {
				at := first.Add(time.Duration(i) * time.Second)
				state := "unchanged"
				if i%2 == 0 {
					state = "errors"
				}
				current := combinedBuildHistoryCurrent(t, at, role, state)
				current.Evidence.EvidenceAfter = first.Add(-time.Minute)
				raw := []string{"all", "none", "http_cron_delivery", "handoff_health", "builder_health"}[i%5]
				current.Diagnostics, _ = operatorobs.ParseRuntimeDiagnostics(role, raw)
				current.DiagnosticsSource = "server"
				if i%7 == 0 {
					current.DiagnosticsSource = "client"
				}
				filterObsDeploymentDiagnostics(current)
				out = r.observe(current, at)
			}
			if out.History.RetainedEvents == 0 || out.History.Events[out.History.RetainedEvents-1].Observation != 80 || out.Progress.HTTPCronDelivery.ObservedIncompleteEvidence || out.Progress.HandoffHealth.ObservedIncompleteEvidence {
				t.Fatal("targeted history lost intent or invented gaps")
			}
			assertBuildHistoryPythonTransport(t, out)
		})
	}
}
