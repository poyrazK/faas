package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"github.com/onebox-faas/faas/pkg/chaos"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestDeadlineBudgetValidationAndProfileCopies(t *testing.T) {
	for _, budget := range []testRequestDeadlineSpec{{Within: "0ms", MinSamples: 1}, {Within: "31s", MinSamples: 1}, {Within: "1.000001ms", MinSamples: 1}, {Within: "1s"}, {Within: "1s", MinSamples: 1, Expect: &testHTTPExpect{Status: 0}}, {Within: "1s", MinSamples: 1, Expect: &testHTTPExpect{Status: 504, JSON: map[string]any{"invalid": "x"}}}} {
		if err := validateTestRequestDeadlines(map[string]testRequestDeadlineSpec{"query": budget}); err == nil {
			t.Fatalf("invalid budget accepted: %+v", budget)
		}
	}
	profile := testAssertionProfile{RequestDeadlines: map[string]testRequestDeadlineSpec{"query": {Within: "1s", MinSamples: 1, Expect: &testHTTPExpect{Status: 504, JSON: map[string]any{"/error": "timeout"}}}}}
	phase := testLoadChaosPhaseSpec{Name: "fault"}
	if err := applyTestAssertionProfile(&phase, "deadline", profile); err != nil {
		t.Fatal(err)
	}
	phase.RequestDeadlines["query"].Expect.JSON["/error"] = "mutated"
	if profile.RequestDeadlines["query"].Expect.JSON["/error"] != "timeout" {
		t.Fatal("profile was shared and mutated")
	}
	if err := applyTestAssertionProfile(&phase, "deadline", profile); err == nil {
		t.Fatal("duplicate assertion did not conflict")
	}
}

func TestRequestDeadlineRejectsOutliersMissingTimingAndWrongContract(t *testing.T) {
	cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{Name: "fault", RequestDeadlines: map[string]testRequestDeadlineSpec{"query": {Within: "100ms", MinSamples: 10, Expect: &testHTTPExpect{Status: 504}}}}}}
	for _, tc := range []struct {
		name              string
		max               float64
		failures, samples int
		pass              bool
	}{{"bounded", 99, 0, 10, true}, {"hidden outlier", 101, 0, 10, false}, {"wrong contract", 50, 1, 10, false}, {"missing timing", 0, 0, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "fault", Steps: []testLoadStepEvidence{{Name: "query", testLoadMetrics: testLoadMetrics{Requests: 10, Failures: tc.failures, Latency: testLoadLatency{Samples: tc.samples, P95MS: 20, MaxMS: tc.max}}}}}}}
			err := applyTestRequestDeadlines(report, cfg)
			if (err == nil) != tc.pass || report.ChaosPhases[0].RequestDeadlines["query"].Passed != tc.pass {
				t.Fatalf("%+v %v", report.ChaosPhases[0].RequestDeadlines, err)
			}
		})
	}
}

func TestRequestDeadlineCompleteResponseOrClientAbort(t *testing.T) {
	for _, tc := range []struct {
		name               string
		headersOnly, wrong bool
		pass               bool
	}{{name: "application timeout", pass: true}, {name: "wrong status", wrong: true}, {name: "body stalls", headersOnly: true}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.wrong {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(504)
				}
				if tc.headersOnly {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte(`{"error":"deadline_exceeded"}`))
			}))
			defer server.Close()
			cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{RequestDeadlines: map[string]testRequestDeadlineSpec{"query": {Within: "100ms", MinSamples: 1, Expect: &testHTTPExpect{Status: 504, JSON: map[string]any{"/error": "deadline_exceeded"}}}}}}}
			original := testHTTPRequest{Name: "query", Method: "GET", Path: "/", Expect: testHTTPExpect{Status: 200}}
			ctx, cancel, step := prepareTestRequestDeadline(context.Background(), cfg, 0, original, time.Now())
			defer cancel()
			result := testHTTPRequestEvidence{Name: "query"}
			err := runOneTestHTTPRequestWithBody(ctx, server.Client(), server.URL, step, nil, nil, nil, nil, &result, true)
			if (err == nil) != tc.pass || original.Expect.Status != 200 {
				t.Fatalf("request: %+v error %v", result, err)
			}
			if tc.headersOnly && ctx.Err() != context.DeadlineExceeded {
				t.Fatal("body consumption was not bounded by overall deadline")
			}
		})
	}
}

func TestRequestDeadlineStreamsKeepPhaseContractsAndCanonicalCorrelation(t *testing.T) {
	run := strings.Repeat("a", 32)
	scope := strings.Repeat("b", 16)
	rule := chaos.Rule{CorrelationScope: scope, From: "app", To: "dependency", Kind: chaos.KindHTTPStatus, StatusCode: 503, Percent: 100}
	var invalid atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query" {
			if _, ok := chaos.RequestCorrelationSlot(run, rule, r.Header.Get(chaos.RequestCorrelationHeader)); !ok {
				invalid.Add(1)
			}
			w.Header().Set(testRequestAttemptsHeader, "1")
			w.WriteHeader(504)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg := &testLoadConfig{Duration: 400 * time.Millisecond, RequestLimit: 100, ErrorRate: 0, Streams: []testLoadStreamSpec{{Name: "dependency", Requests: []string{"query"}, Rate: 10, VUs: 2}, {Name: "healthy", Requests: []string{"health"}, Rate: 10, VUs: 2}}, ChaosSchedule: []testLoadChaosPhase{{Name: "fault", Duration: 400 * time.Millisecond, RequestDeadlines: map[string]testRequestDeadlineSpec{"query": {Within: "100ms", MinSamples: 3, Expect: &testHTTPExpect{Status: 504}}}, Rules: []testChaosRule{{CorrelationScope: scope}}, RetryAmplification: &testRetryAmplificationSpec{Request: "query", PerRequest: true}}}}
	cfg.ChaosPhaseClock = newTestLoadPhaseClock(1)
	cfg.ChaosPhaseClock.start(time.Now())
	cfg.ChaosPhaseClock.finish(time.Now().Add(time.Second))
	requests := []testHTTPRequest{{Name: "query", Method: "GET", Path: "/query", Expect: testHTTPExpect{Status: 200}}, {Name: "health", Method: "GET", Path: "/", Expect: testHTTPExpect{Status: 200}}}
	report, err := runTestLoad(context.Background(), server.URL, run, nil, requests, nil, cfg)
	if err != nil || invalid.Load() != 0 || report.Failures != 0 {
		t.Fatalf("stream phase contract or namespace lost: %v invalid=%d %+v", err, invalid.Load(), report)
	}
	if len(report.ChaosPhases[0].CorrelationRequests) < 3 {
		t.Fatal("stream did not retain per-request correlation receipts")
	}
	if err := applyTestRequestDeadlines(report, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRequestDeadlineExampleReplayAndReports(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-checkout/gregale-request-deadlines-test.yaml", "request-deadlines", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("example: %v", err)
	}
	source := prepared[0].Scenario
	if _, err := collectTestValidationForEngine(map[string]testScenario{prepared[0].Name: source}, prepared[0].ManifestDir, prepared[0].Name, prepared[0].Engine); err != nil {
		t.Fatal(err)
	}
	cfg := outageCyclesConfig(t, source)
	if cfg.ChaosSchedule[0].RequestDeadlines["dependency-probe"].Expect.Status != 200 || cfg.ChaosSchedule[1].RequestDeadlines["dependency-probe"].Expect.Status != 504 || source.Steps[1].Load.ChaosSchedule[0].RequestDeadlines["dependency-probe"].Expect != nil {
		t.Fatal("runtime contracts modified their source")
	}
	report := outageCyclesReceipt(t, cfg)
	for i := range report.ChaosPhases {
		report.ChaosPhases[i].Steps = []testLoadStepEvidence{{Name: "dependency-probe", testLoadMetrics: testLoadMetrics{Requests: 10, Latency: testLoadLatency{Samples: 10, MaxMS: 500}}}}
	}
	if err := applyTestRequestDeadlines(report, cfg); err != nil {
		t.Fatal(err)
	}
	receipt := testRunReceipt{Scenario: "request-deadlines", Engine: "real-vm", Profile: "warm", Status: "passed", RunID: "deadline", Load: report, Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}, Replay: newTestReplayManifest("request-deadlines", "warm", "", source)}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "500.000ms / 750.000ms") {
		t.Fatal("terminal lost deadline")
	}
	html := filepath.Join(t.TempDir(), "results.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !bytes.Contains(body, []byte("Request deadline dependency-probe")) {
		t.Fatal("HTML lost deadlines")
	}
	junit := filepath.Join(t.TempDir(), "results.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(junit)
	var suite testJUnitSuite
	_ = xml.Unmarshal(body, &suite)
	var decoded testRunReceipt
	_ = json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded)
	if decoded.Load.ChaosPhases[1].RequestDeadlines["dependency-probe"].ExpectedStatus != 504 {
		t.Fatal("JSON/JUnit lost timeout contract")
	}
	altered := source
	altered.Steps = append([]testScenarioStep(nil), source.Steps...)
	load := *source.Steps[1].Load
	load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
	load.ChaosSchedule[1].RequestDeadlines = map[string]testRequestDeadlineSpec{"dependency-probe": {Within: "1s", MinSamples: 3}}
	altered.Steps[1].Load = &load
	if _, err := prepareTestReplay(&receipt, map[string]testScenario{"request-deadlines": altered}, prepared[0].ManifestDir); err == nil {
		t.Fatal("changed deadline escaped replay binding")
	}
	unknown := *source.Steps[1].Load
	unknown.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), unknown.ChaosSchedule...)
	unknown.ChaosSchedule[0].RequestDeadlines = map[string]testRequestDeadlineSpec{"unknown": {Within: "1s", MinSamples: 1}}
	if _, err := resolveTestLoadConfig(testScenario{Requests: source.Steps[1].Requests, Load: &unknown}, testLoadOverrides{}); err == nil {
		t.Fatal("unknown named request accepted")
	}
}
