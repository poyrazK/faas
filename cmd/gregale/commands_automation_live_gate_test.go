// adr: 1299
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

const gateCLIBaseline = "00000000-0000-4000-8000-000000000010"
const gateCLICandidate = "00000000-0000-4000-8000-000000000011"

func automationGateArgs() []string {
	return []string{"automations", "live", "gate", "--app", "billing", "--name", "case/report", "--baseline", gateCLIBaseline, "--candidate", gateCLICandidate,
		"--expected-version", "7", "--expected-published-version", "4", "--expected-deployment-id", liveCLIDeployment}
}

func automationGateView() api.AutomationLiveSuiteComparison {
	now := time.Now().UTC()
	summary := api.AutomationLiveSuiteSummary{Name: "case/report", Version: 7, PublishedVersion: 4, DeploymentID: liveCLIDeployment, CreatedAt: now, UpdatedAt: now, CaseCount: 1, LinkedRuns: 1}
	view := api.AutomationLiveSuiteComparison{Baseline: summary, Candidate: summary, BaselineOutdated: true, ObservedAt: now, Summary: api.AutomationLiveSuiteComparisonCounts{Total: 1, Unchanged: 1}, Cases: []api.AutomationLiveSuiteCaseComparison{
		{Name: "happy", Change: "unchanged", Baseline: &api.AutomationLiveSuiteObservation{RunID: liveCLIRun, Result: "passed"}, Candidate: &api.AutomationLiveSuiteObservation{RunID: "00000000-0000-4000-8000-000000000012", Result: "passed"}},
	}}
	view.Baseline.ID, view.Candidate.ID = gateCLIBaseline, gateCLICandidate
	return view
}

func TestAutomationGateCLIReadOnlyReportsAndErrors(t *testing.T) {
	for _, mode := range []string{"passed", "unchanged-failure", "regression", "pending", "incompatible", "outdated", "wrong-target", "wrong-id", "wrong-name", "false-counts", "future-result", "missing-freshness", "empty", "malformed", "401", "403", "404", "410", "409", "429", "503"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			f := authedFakeAPI(t, "", http.StatusOK)
			var calls atomic.Int32
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				want := "/v1/apps/billing/automations/case%2Freport/live-suites/" + gateCLICandidate + "/compare/" + gateCLIBaseline
				if r.Method != "GET" || r.URL.EscapedPath() != want || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Idempotency-Key") != "" || r.ContentLength > 0 {
					t.Error("gate changed execution, target or read-only request contract", r.Method, r.URL.EscapedPath())
				}
				w.Header().Set("Content-Type", "application/json")
				if status := map[string]int{"401": 401, "403": 403, "404": 404, "410": 410, "409": 409, "429": 429, "503": 503}[mode]; status != 0 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"detail":"PRIVATE backend error","code":"PRIVATE-code"}`))
					return
				}
				if mode == "empty" {
					return
				}
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"PRIVATE malformed`))
					return
				}
				view := automationGateView()
				switch mode {
				case "unchanged-failure":
					view.Cases[0].Baseline.Result, view.Cases[0].Candidate.Result = "failed", "failed"
				case "regression":
					view.Cases[0].Candidate.Result, view.Cases[0].Change = "failed", "regression"
					view.Summary.Unchanged, view.Summary.Regressions = 0, 1
				case "pending":
					view.Cases[0].Candidate.Result, view.Cases[0].Change = "pending", "unknown"
					view.Summary.Unchanged, view.Summary.Unknown = 0, 1
				case "incompatible":
					view.Cases[0].Change = "incompatible"
					view.Summary.Unchanged, view.Summary.Incompatible = 0, 1
				case "outdated":
					view.CandidateOutdated = true
				case "wrong-target":
					view.Candidate.Version++
				case "wrong-id":
					view.Baseline.ID = liveCLIDeployment
				case "wrong-name":
					view.Candidate.Name = "PRIVATE foreign"
				case "false-counts":
					view.Candidate.LinkedRuns = 0
				case "future-result":
					view.Cases[0].Candidate.Result = "PRIVATE future"
				}
				raw, _ := json.Marshal(view)
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				fields["input"], fields["output"], fields["raw_error"] = "PRIVATE input", "PRIVATE output", "PRIVATE error"
				if mode == "missing-freshness" {
					delete(fields, "candidate_outdated")
				}
				_ = json.NewEncoder(w).Encode(fields)
			})
			code, stderr := runWithStderr(t, func() int { return run(automationGateArgs()) })
			want, outcome := 0, "passed"
			switch mode {
			case "unchanged-failure", "regression":
				want, outcome = 1, "failed"
			case "pending", "incompatible", "outdated", "wrong-target":
				want, outcome = 7, "inconclusive"
			case "wrong-id", "wrong-name", "false-counts", "future-result", "missing-freshness", "empty", "malformed":
				want, outcome = 3, "error"
			case "401", "403", "404", "410", "409", "429", "503":
				want, outcome = map[string]int{"401": 2, "403": 6, "404": 4, "410": 4, "409": 5, "429": 3, "503": 3}[mode], "error"
			}
			var report automationLiveGateReport
			if code != want || json.Unmarshal(out.Bytes(), &report) != nil || report.SchemaVersion != 1 || report.Policy != "strict_v1" || report.ExitCode != code || report.Outcome != outcome || report.BaselineID != gateCLIBaseline || report.CandidateID != gateCLICandidate || report.Expected.PublishedVersion != 4 || calls.Load() != 1 {
				t.Fatalf("exit=%d want=%d report=%s stderr=%s calls=%d", code, want, out, stderr, calls.Load())
			}
			if strings.Contains(out.String()+stderr, "PRIVATE") || strings.Contains(out.String()+stderr, "test-token") {
				t.Fatal("gate exposed a payload, token, or raw backend error")
			}
			if outcome == "error" {
				if report.Comparison != nil || stderr == "" {
					t.Fatal("error trusted malformed comparison or lost its diagnostic")
				}
			} else if report.Comparison == nil || stderr != "" {
				t.Fatal("policy decision did not retain evidence or was treated as a request error")
			}
		})
	}
}

func TestAutomationGateCLIRejectsUnreviewedOptionsLocally(t *testing.T) {
	for _, extra := range [][]string{{"--baseline", gateCLICandidate}, {"--candidate", "bad"}, {"--expected-version", "0"}, {"--expected-published-version", "8"}, {"--expected-version", "PRIVATE"}, {"--expected-deployment-id", ""}, {"--case", "override"}, {"--confirm-real-actions"}, {"--wait"}, {"unexpected"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			f := authedFakeAPI(t, "", 200)
			var calls atomic.Int32
			f.srv.Config.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			code, stderr := runWithStderr(t, func() int { return run(append(automationGateArgs(), extra...)) })
			if code != 1 || out.Len() != 0 || calls.Load() != 0 || strings.Contains(stderr, "PRIVATE") {
				t.Fatal("invalid gate options reached API or exposed values", code, out, stderr)
			}
		})
	}
}

func TestAutomationGateCLIHumanOutputAndManifestHelp(t *testing.T) {
	resetJSONOut(t)
	out := captureAutomationStdout(t)
	f := authedFakeAPI(t, "", 200)
	var calls atomic.Int32
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(automationGateView())
	})
	if code := run([]string{"automations", "live", "gate", "--help"}); code != 0 || calls.Load() != 0 {
		t.Fatal("gate help contacted API", code)
	}
	for _, flag := range []string{"--baseline", "--candidate", "--expected-version", "--expected-published-version", "--expected-deployment-id"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatal("missing help flag", flag)
		}
	}
	out.Reset()
	if code := run(automationGateArgs()); code != 0 || !strings.Contains(out.String(), "Automation gate passed") || !strings.Contains(out.String(), "happy: unchanged (baseline passed, candidate passed)") || strings.Contains(out.String(), "{\n") {
		t.Fatal("human gate output missing decision or case evidence", code, out)
	}
}

func TestAutomationGateCLIInterruptionAndDeadlineAreReadOnly(t *testing.T) {
	for _, mode := range []string{"before-request", "during-request", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			out := captureAutomationStdout(t)
			f := authedFakeAPI(t, "", 200)
			o, err := parseAutomationLiveGate(automationGateArgs()[3:])
			if err != nil {
				t.Fatal(err)
			}
			client, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer stop()
			}
			var calls atomic.Int32
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" {
					t.Error("interrupted gate mutated execution")
				}
				if mode == "during-request" {
					cancel()
				}
				<-r.Context().Done()
			})
			if mode == "before-request" {
				cancel()
			}
			code, stderr := runWithStderr(t, func() int { return runAutomationLiveGate(ctx, client, o) })
			want := 130
			if mode == "deadline" {
				want = 3
			}
			var report automationLiveGateReport
			if code != want || json.Unmarshal(out.Bytes(), &report) != nil || report.ExitCode != want || report.Comparison != nil || stderr == "" || mode == "before-request" && calls.Load() != 0 {
				t.Fatal(code, out, stderr, calls.Load())
			}
		})
	}
}

type automationGateBrokenWriter struct{}

func (automationGateBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE writer failure")
}

func TestAutomationGateCLIOutputFailureCannotPass(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	previous := osStdout
	osStdout = automationGateBrokenWriter{}
	t.Cleanup(func() { osStdout = previous })
	o, err := parseAutomationLiveGate(automationGateArgs()[3:])
	if err != nil {
		t.Fatal(err)
	}
	code, stderr := runWithStderr(t, func() int {
		return renderAutomationLiveGate(o, nil, automationgate.Result{Outcome: "passed"}, "passed")
	})
	if code != 3 || strings.Contains(stderr, "PRIVATE") {
		t.Fatal("unwritten report passed or leaked writer details", code, stderr)
	}
}
