package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type scenarioRestartFake struct {
	row        api.RuntimeConfigRestartStatusResponse
	requestID  string
	err        error
	requests   int
	polls      int
	slug       string
	onRestart  func()
	bootMethod string
}

func (f *scenarioRestartFake) ListWakeTimeline(_ context.Context, _, wakeID, _ string, _ int) (api.WakeTimelineResponse, error) {
	method := f.bootMethod
	if method == "" {
		method = "cold_boot"
	}
	return api.WakeTimelineResponse{WakeID: wakeID, Events: []api.WakeTimelineEvent{{Kind: "wake.boot_completed", Data: map[string]any{"method": method}}}}, nil
}

func (f *scenarioRestartFake) RestartAppFresh(_ context.Context, slug string) (api.AppRestartResponse, error) {
	f.requests++
	f.slug = slug
	if f.onRestart != nil {
		f.onRestart()
	}
	return api.AppRestartResponse{WakeID: f.requestID}, f.err
}

func (f *scenarioRestartFake) GetRuntimeConfigRestartStatus(_ context.Context, _, _ string) (api.RuntimeConfigRestartStatusResponse, error) {
	f.polls++
	return f.row, nil
}

func restartScenarioSpec() testRestartSpec {
	return testRestartSpec{
		Workload: "app", Timeout: "1s",
		Readiness:     testHTTPRequest{Name: "ready", Method: "GET", Path: "/ready", Expect: testHTTPExpect{Status: 200}},
		StorageChecks: []testHTTPRequest{{Name: "ledger", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200, JSON: map[string]any{"/id": "${steps.seed.id}"}}}},
	}
}

func TestScenarioRestartFailsClosedAndSubmitsOnce(t *testing.T) {
	for _, mode := range []string{"success", "pre-storage", "lost-storage", "request-error", "missing-id", "wrong-receipt", "failed", "missing-completion", "readiness", "queued", "cancelled", "service", "wrong-boot"} {
		t.Run(mode, func(t *testing.T) {
			row := restartStatusTestReceipt("completed")
			fake := &scenarioRestartFake{row: row, requestID: row.WakeID}
			ledger, restarted, readyCalls := "private-ledger-value", false, 0
			var mu sync.Mutex
			fake.onRestart = func() {
				mu.Lock()
				defer mu.Unlock()
				restarted = true
				if mode == "lost-storage" {
					ledger = "replacement-ledger"
				}
			}
			switch mode {
			case "pre-storage":
				ledger = "missing"
			case "request-error":
				fake.err = errors.New("unavailable")
			case "missing-id":
				fake.requestID = ""
			case "wrong-receipt":
				fake.row.WakeID = restartStatusTestReceipt("completed").WakeID
			case "failed":
				fake.row.Status = "failed"
			case "queued":
				fake.row.Status = "queued"
			case "missing-completion":
				fake.row.CompletedAt = nil
			case "wrong-boot":
				fake.bootMethod = "restore"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/ready" {
					readyCalls++
					if !restarted || mode == "readiness" || mode == "success" && readyCalls == 1 {
						w.WriteHeader(503)
						return
					}
					w.WriteHeader(200)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"id": ledger})
			}))
			defer server.Close()
			spec := restartScenarioSpec()
			env := []string{"GREGALE_TEST_APP_SLUG=checkout-test"}
			if mode == "service" {
				spec.Workload = "payment"
				env = append(env, "GREGALE_TEST_SERVICE_PAYMENT_APP_SLUG=payment-test", "GREGALE_TEST_SERVICE_PAYMENT_URL="+server.URL)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "readiness" || mode == "queued" || mode == "wrong-boot" {
				ctx, cancel = context.WithTimeout(ctx, 40*time.Millisecond)
				defer cancel()
			}
			if mode == "cancelled" {
				cancel()
			}
			evidence, err := runTestRestart(ctx, fake, server.URL, env, nil, "run", spec, map[string]string{"steps.seed.id": "private-ledger-value"})
			if mode == "success" || mode == "service" {
				if err != nil || evidence.Status != "passed" || len(evidence.Before) != 1 || len(evidence.After) != 1 || fake.requests != 1 {
					t.Fatalf("restart = %+v, %v; requests %d", evidence, err, fake.requests)
				}
				if mode == "success" && evidence.ReadinessAttempts != 2 {
					t.Fatal("readiness must be verified after processing, including retry")
				}
				if mode == "service" && fake.slug != "payment-test" {
					t.Fatal("restart did not use the declared service target")
				}
			} else if err == nil || evidence.Status != "failed" {
				t.Fatalf("failure passed: %+v, %v", evidence, err)
			}
			if fake.requests > 1 || (mode == "pre-storage" || mode == "cancelled") && fake.requests != 0 {
				t.Fatal("restart resubmitted or requested despite a failed precondition")
			}
			encoded, _ := json.Marshal(evidence)
			if strings.Contains(string(encoded), "private-ledger-value") {
				t.Fatal("restart report exposed persisted JSON values")
			}
		})
	}
}

func TestScenarioRestartFailureClearsActiveFault(t *testing.T) {
	row := restartStatusTestReceipt("failed")
	var cleared, restarted atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/chaos"):
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "generation", RulesInstalled: 1})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/chaos/matches"):
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "generation"})
		case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/chaos"):
			cleared.Add(1)
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/checkout-test/restart" && r.URL.Query().Get("fresh") == "true":
			restarted.Add(1)
			_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: row.WakeID})
		case strings.Contains(r.URL.Path, "/runtime-config-restarts/"):
			_ = json.NewEncoder(w).Encode(row)
		default:
			w.WriteHeader(404)
		}
	}))
	defer backend.Close()
	var active atomic.Bool
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/write" {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
			active.Store(true)
			defer active.Store(false)
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/checkpoint" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"pending": active.Load()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"retained-ledger"}`))
	}))
	defer app.Close()
	probe := testHTTPRequest{Name: "seed", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200}, Capture: map[string]string{"id": "/id"}}
	restart := restartScenarioSpec()
	restart.InFlight = restartFlightSpec()
	steps := []testScenarioStep{
		{Name: "baseline", Requests: []testHTTPRequest{probe}},
		{Name: "fault", Chaos: &testChaosSpec{Duration: "1m", Rules: []testChaosRule{{To: "payment", Kind: "http_status", Percent: 100, StatusCode: 503}}}, Requests: []testHTTPRequest{{Name: "fault-probe", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200}}}},
		{Name: "restart", Restart: &restart},
		{Name: "clear", ClearChaos: true},
	}
	receipt := testRunReceipt{}
	err := runTestScenarioSteps(context.Background(), NewClient(backend.URL, "token"), t.TempDir(), app.URL, []string{"GREGALE_TEST_APP_SLUG=checkout-test"}, nil, "run", steps, map[string]string{}, &receipt)
	if err == nil || cleared.Load() != 1 || restarted.Load() != 1 || len(receipt.Steps) != 3 || receipt.Steps[2].Restart == nil || receipt.Steps[2].Restart.Status != "failed" || !receipt.Steps[2].Restart.InFlight.Joined || receipt.Steps[2].Restart.InFlight.Outcome != "canceled_on_failure" {
		t.Fatalf("restart cleanup: %v; cleared %d, restarted %d, steps %+v", err, cleared.Load(), restarted.Load(), receipt.Steps)
	}
}

func TestScenarioRestartValidation(t *testing.T) {
	for _, mode := range []string{"valid", "first", "undeclared", "command", "post", "capture", "empty-storage", "no-json", "bad-timeout", "unhealthy", "unknown-reference"} {
		t.Run(mode, func(t *testing.T) {
			spec := restartScenarioSpec()
			scenario := testScenario{Timeout: "5m", Steps: []testScenarioStep{{Requests: []testHTTPRequest{{Name: "seed", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200}, Capture: map[string]string{"id": "/id"}}}}}}
			step, index := testScenarioStep{Name: "restart", Restart: &spec}, 1
			switch mode {
			case "first":
				index = 0
			case "undeclared":
				spec.Workload = "production-app"
			case "command":
				step.Command = []string{"true"}
			case "post":
				spec.Readiness.Method = "POST"
			case "capture":
				spec.Readiness.Capture = map[string]string{"new": "/id"}
			case "empty-storage":
				spec.StorageChecks = nil
			case "no-json":
				spec.StorageChecks[0].Expect.JSON = nil
			case "bad-timeout":
				spec.Timeout = "6m"
			case "unhealthy":
				spec.Readiness.Expect.Status = 503
			case "unknown-reference":
				spec.StorageChecks[0].Expect.JSON["/id"] = "${steps.missing.id}"
			}
			err := validateTestRestart(scenario, index, step, nil)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestScenarioRestartReportEvidence(t *testing.T) {
	receipt := testRunReceipt{Scenario: "checkout", Status: "failed", Error: "restart storage check failed", Steps: []testScenarioStepEvidence{{Name: "restart-checkout", Status: "failed", Restart: &testRestartEvidence{InFlight: &testRestartInFlightEvidence{Name: "held-write", CheckpointAttempts: 2, PendingAtRestart: true, Joined: true, Outcome: "canceled_on_failure"}, Workload: "app", WakeID: "restart-receipt", Status: "failed", ProcessingStatus: "completed", Before: []testHTTPRequestEvidence{{Name: "ledger", Passed: true}}, After: []testHTTPRequestEvidence{{Name: "ledger", Passed: false}}}}}}
	dir := t.TempDir()
	html, junit := filepath.Join(dir, "report.html"), filepath.Join(dir, "report.xml")
	if err := writeTestHTMLReport(html, "checkout", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	if err := writeTestJUnit(junit, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{html, junit} {
		body, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(body), "restart-receipt") || !strings.Contains(string(body), "failed") || !strings.Contains(string(body), "held-write") || !strings.Contains(string(body), "canceled_on_failure") {
			t.Fatalf("restart evidence missing in %s: %v", path, err)
		}
	}
}

func TestScenarioRestartBaselineAllowsOnlyVerifiedPlannedWakes(t *testing.T) {
	original := map[string]bool{"initial": true}
	steps := []testScenarioStepEvidence{
		{Restart: &testRestartEvidence{Workload: "payment", WakeID: "planned", Status: "passed", ProcessingStatus: "completed", BootMethod: "cold_boot"}},
		{Restart: &testRestartEvidence{Workload: "payment", WakeID: "failed", Status: "failed", ProcessingStatus: "completed", BootMethod: "cold_boot"}},
		{Restart: &testRestartEvidence{Workload: "other", WakeID: "other", Status: "passed", ProcessingStatus: "completed", BootMethod: "cold_boot"}},
		{Restart: &testRestartEvidence{Workload: "payment", WakeID: "restored", Status: "passed", ProcessingStatus: "completed", BootMethod: "restore"}},
	}
	baseline, planned := testServiceRestartBaseline(original, steps, "payment")
	if len(planned) != 1 || planned[0] != "planned" || len(baseline) != 2 || len(original) != 1 {
		t.Fatalf("invalid planned-wake allowance: %v, %v, original %v", baseline, planned, original)
	}
	rows := []api.WakeTimelineJSONRow{{WakeID: "unexpected"}, {WakeID: "planned"}, {WakeID: "initial"}}
	if firstNewTestWakeID(rows, baseline) != "unexpected" {
		t.Fatal("planned restart masked an unrelated wake")
	}
}
