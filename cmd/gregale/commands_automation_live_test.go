// adr: 1294
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const liveCLIRun = "00000000-0000-4000-8000-000000000001"
const liveCLIDeployment = "00000000-0000-4000-8000-000000000002"

func liveCLIView(status string) api.AutomationLiveVerification {
	return api.AutomationLiveVerification{RunID: liveCLIRun, CaseName: "synthetic", DeploymentID: liveCLIDeployment,
		RunStatus: status, Version: 7, PublishedVersion: 4, CreatedAt: time.Now().UTC()}
}

func liveCLIStartArgs() []string {
	return []string{"automations", "live", "start", "--app", "billing", "--name", "report", "--case", "synthetic",
		"--expected-version", "7", "--expected-published-version", "4", "--expected-deployment-id", liveCLIDeployment,
		"--idempotency-key", "ci-synthetic-1", "--confirm-real-actions"}
}

func TestAutomationLiveCLIResultsAndRoutes(t *testing.T) {
	for _, mode := range []string{"accepted", "start-wait", "wait", "show", "check", "current", "expected-failure", "failed", "outdated", "pending", "changed", "resumed", "wrong-run", "wrong-start", "unknown-state", "malformed", "timeout", "401", "403", "404", "409", "503"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			output := captureAutomationStdout(t)
			f := authedFakeAPI(t, "", http.StatusOK)
			var mu sync.Mutex
			var calls []string
			reads, checks := 0, 0
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authentication")
				}
				if len(mode) == 3 && mode[0] >= '4' && mode[0] <= '5' {
					status := map[string]int{"401": 401, "403": 403, "404": 404, "409": 409, "503": 503}[mode]
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"private_error","detail":"PRIVATE backend payload"}`))
					return
				}
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"PRIVATE malformed`))
					return
				}
				view := liveCLIView("succeeded")
				switch {
				case r.Method == "POST" && !strings.HasSuffix(r.URL.Path, "/check"):
					if r.Header.Get("Idempotency-Key") != "ci-synthetic-1" || r.URL.Path != "/v1/apps/billing/automations/report/live-verifications" {
						t.Error("start target or stable key changed")
					}
					var body api.StartAutomationLiveVerificationRequest
					decoder := json.NewDecoder(r.Body)
					decoder.DisallowUnknownFields()
					if decoder.Decode(&body) != nil || !body.ConfirmRealActions || body.ExpectedVersion != 7 || body.ExpectedPublishedVersion != 4 || body.ExpectedDeploymentID != liveCLIDeployment || body.CaseName != "synthetic" {
						t.Errorf("unexpected start body: %+v", body)
					}
					view.RunStatus = "pending"
					if mode == "wrong-start" {
						view.CaseName = "other"
					}
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/check"):
					checks++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 0 {
						t.Error("check submitted execution or editor data")
					}
					view.ReportCurrent = mode != "changed"
					view.Report = &api.AutomationLiveReport{RunStatus: "succeeded", Passed: mode != "failed", CheckedAt: time.Now().UTC(),
						Steps: []api.AutomationTestStepResult{{Step: "collect", State: "succeeded", Passed: mode != "failed", Mismatches: []string{"state"}}}}
					if mode == "resumed" && checks == 1 {
						view.RunStatus, view.ReportCurrent, view.Report = "running", false, nil
					}
				default:
					reads++
					if mode == "start-wait" && reads < 3 {
						view.RunStatus = []string{"running", "awaiting_event"}[reads-1]
					}
					if mode == "show" || mode == "pending" || mode == "timeout" || mode == "wait" && reads == 1 {
						view.RunStatus = "pending"
					}
					if mode == "current" || mode == "outdated" || mode == "expected-failure" {
						view.ReportCurrent = true
						view.Outdated = mode == "outdated"
						if mode == "expected-failure" {
							view.RunStatus = "failed"
						}
						view.Report = &api.AutomationLiveReport{Passed: true, RunStatus: view.RunStatus}
					}
					if mode == "wrong-run" {
						view.RunID = liveCLIDeployment
					}
					if mode == "unknown-state" {
						view.RunStatus = "unknown"
					}
				}
				// Unknown payload fields must never become part of the safe CLI report.
				data, _ := json.Marshal(view)
				var fields map[string]any
				_ = json.Unmarshal(data, &fields)
				fields["input"], fields["output"], fields["raw_error"] = "PRIVATE input", "PRIVATE output", "PRIVATE error"
				_ = json.NewEncoder(w).Encode(fields)
			})
			verb := "check"
			if mode == "show" || mode == "wait" || mode == "timeout" || mode == "resumed" {
				verb = "show"
				if mode != "show" {
					verb = "wait"
				}
			}
			args := []string{"automations", "live", verb, "--app", "billing", "--name", "report", "--run", liveCLIRun}
			if mode == "accepted" || mode == "start-wait" || mode == "wrong-start" {
				args = liveCLIStartArgs()
				if mode == "start-wait" {
					args = append(args, "--wait", "--poll-interval", "1s")
				}
			} else if verb == "wait" {
				args = append(args, "--poll-interval", "1s")
				if mode == "timeout" {
					args = append(args, "--timeout", "40ms")
				}
			}
			code, stderr := runWithStderr(t, func() int { return run(args) })
			wantCode, wantOutcome := 0, "passed"
			switch mode {
			case "accepted":
				wantOutcome = "accepted"
			case "show":
				wantOutcome = "observed"
			case "failed", "outdated", "pending":
				wantCode, wantOutcome = 1, mode
			case "changed":
				wantCode, wantOutcome = 1, "unavailable"
			case "wrong-run", "wrong-start", "unknown-state", "malformed":
				wantCode, wantOutcome = 3, "error"
			case "timeout":
				wantCode, wantOutcome = 124, "timed_out"
			case "401", "403", "404", "409", "503":
				wantCode, wantOutcome = map[string]int{"401": 2, "403": 6, "404": 4, "409": 5, "503": 3}[mode], "error"
			}
			var report automationLiveResult
			if json.Unmarshal(output.Bytes(), &report) != nil || report.SchemaVersion != 1 || report.App != "billing" || report.Name != "report" || report.Outcome != wantOutcome || code != wantCode || report.ExitCode != wantCode {
				t.Fatalf("exit=%d want=%d outcome=%s stdout=%s stderr=%s", code, wantCode, wantOutcome, output, stderr)
			}
			if strings.Contains(output.String()+stderr, "PRIVATE") {
				t.Fatal("CLI report exposed a payload or backend error")
			}
			if report.Verification != nil && !strings.Contains(report.Reconnect, "automations live wait --app") {
				t.Fatal("known run has no reconnect command")
			}
			if verb != "start" && args[2] != "start" && (report.RunID != liveCLIRun || !strings.Contains(report.Reconnect, liveCLIRun)) {
				t.Fatal("failed initial read lost the requested run identity", output.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "start-wait" && (len(calls) != 5 || checks != 1) || mode == "accepted" && len(calls) != 1 || mode == "resumed" && checks != 2 || mode == "outdated" && checks != 0 || mode == "current" && checks != 0 {
				t.Fatalf("unexpected extra work: %v", calls)
			}
			for _, call := range calls {
				if strings.Contains(call, "resume") || strings.Contains(call, "cancel") {
					t.Fatal("observation changed execution")
				}
			}
		})
	}
}

func TestAutomationLiveCLIStartRetryPreservesOriginalRequest(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	out := captureAutomationStdout(t)
	f := authedFakeAPI(t, "", 200)
	var mu sync.Mutex
	var bodies []string
	var keys []string
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/v1/apps/billing/automations/report/live-verifications" {
			t.Error("retry looked up a new target or performed another operation")
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"detail":"PRIVATE uncertain admission"}`))
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(liveCLIView("pending"))
	})
	args := liveCLIStartArgs()[2:]
	if code, stderr := runWithStderr(t, func() int { return cmdAutomationsLive(args) }); code != 3 || strings.Contains(out.String()+stderr, "PRIVATE") {
		t.Fatal("uncertain start was not reported safely", code, out.String(), stderr)
	}
	out.Reset()
	if code := cmdAutomationsLive(args); code != 0 {
		t.Fatal("explicit retry failed", code, out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0] != bodies[1] || !reflect.DeepEqual(keys, []string{"ci-synthetic-1", "ci-synthetic-1"}) {
		t.Fatal("retry changed original fences or stable key", bodies, keys)
	}
}

func TestAutomationLiveCLIValidationAndHelp(t *testing.T) {
	for _, args := range [][]string{
		nil, {"bad"}, {"start", "--app", "billing", "--name", "report"},
		{"wait", "--app", "billing", "--name", "report", "--run", "bad"},
		{"wait", "--app", "billing", "--name", "report", "--run", liveCLIRun, "--timeout", "2h"},
		{"wait", "--app", "billing", "--name", "report", "--run", liveCLIRun, "--poll-interval", "1ms"},
		{"wait", "--app", "billing", "--name", "report", "--run", liveCLIRun, "--poll-interval", "2m"},
		{"wait", "--app", "billing", "--name", "report", "--run", liveCLIRun, "--confirm-real-actions"},
		{"show", "--app", "billing", "--name", "report", "--run", liveCLIRun, "extra"},
		append(liveCLIStartArgs()[2:], "--confirm-real-actions=false"),
		append(liveCLIStartArgs()[2:], "--timeout", "1m"),
		append(liveCLIStartArgs()[2:], "--idempotency-key", "bad\nkey"),
		append(liveCLIStartArgs()[2:], "--expected-deployment-id", "00000000-0000-0000-0000-000000000000"),
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, "", 200)
			if code, _ := runWithStderr(t, func() int { return cmdAutomationsLive(args) }); code != 1 || f.sawMethod != "" {
				t.Fatal("invalid options reached the API", code, f.sawMethod)
			}
		})
	}
	resetJSONOut(t)
	f := authedFakeAPI(t, "", 200)
	out := captureAutomationStdout(t)
	for _, verb := range []string{"start", "list", "show", "check", "wait"} {
		if code := run([]string{"automations", "live", verb, "--help"}); code != 0 || f.sawMethod != "" {
			t.Fatal("help admitted a request", verb, code)
		}
	}
	if !strings.Contains(out.String(), "--confirm-real-actions") || !strings.Contains(out.String(), "--poll-interval") {
		t.Fatal("new flags missing from manifest help")
	}
}

func TestAutomationLiveCLIInterruptedWaitPreservesIdentity(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	out := captureAutomationStdout(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	view := liveCLIView("running")
	code := waitAutomationLive(ctx, nil, automationLiveOptions{app: "billing", name: "report", wait: true}, view)
	var result automationLiveResult
	if json.Unmarshal(out.Bytes(), &result) != nil || code != 130 || result.ExitCode != 130 || result.Outcome != "interrupted" || result.Verification.RunID != liveCLIRun || !strings.Contains(result.Reconnect, liveCLIRun) {
		t.Fatal("interrupt lost reconnect identity", code, out.String())
	}
}

func TestAutomationLiveCLIListAndHumanReport(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		t.Run(map[bool]string{true: "json", false: "human"}[jsonMode], func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = jsonMode
			out := captureAutomationStdout(t)
			listing := api.ListAutomationLiveVerificationsResponse{DeploymentID: liveCLIDeployment, PublishedVersion: 4, Verifications: []api.AutomationLiveVerification{liveCLIView("running")}}
			body, _ := json.Marshal(listing)
			f := authedFakeAPI(t, string(body), 200)
			if code := cmdAutomationsLive([]string{"list", "--app", "billing", "--name", "report"}); code != 0 || f.sawMethod != "GET" || !strings.HasSuffix(f.sawPath, "/live-verifications") {
				t.Fatal("list failed", code, out.String())
			}
			if jsonMode {
				var got struct {
					App, Name string
					api.ListAutomationLiveVerificationsResponse
				}
				if json.Unmarshal(out.Bytes(), &got) != nil || got.App != "billing" || got.Name != "report" || !reflect.DeepEqual(got.Verifications, listing.Verifications) {
					t.Fatal("list JSON mismatch", out.String())
				}
			} else {
				view := liveCLIView("failed")
				view.ReportCurrent, view.Report = true, &api.AutomationLiveReport{RunStatus: "failed", Passed: false, Steps: []api.AutomationTestStepResult{{Step: "collect", Mismatches: []string{"state"}}}}
				if code := renderAutomationLive(automationLiveOptions{app: "billing", name: "report"}, &view, "failed", "Live verification failed.", 1); code != 1 || !strings.Contains(out.String(), "mismatches=state") {
					t.Fatal("human mismatch summary missing", out.String())
				}
			}
		})
	}
}
