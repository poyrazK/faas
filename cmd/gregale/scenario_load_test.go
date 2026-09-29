package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func loadTestStep() testHTTPRequest {
	return testHTTPRequest{Name: "ping", Method: "GET", Path: "/ping", Expect: testHTTPExpect{Status: 200}}
}

func TestLoadConfigurationAndOverrides(t *testing.T) {
	scenario := testScenario{Requests: []testHTTPRequest{loadTestStep()}}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil || cfg.VUs != 1 || cfg.Iterations != 100 || cfg.ErrorRate != 0 || cfg.RequestLimit != 100000 {
		t.Fatalf("default config = %+v, %v", cfg, err)
	}
	rate := 0.01
	scenario.Load = &testLoadSpec{VUs: 5, Duration: "30s", Thresholds: testLoadThresholds{P95: "250ms", ErrorRate: &rate}}
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Iterations: 20, IterationsSet: true})
	if err != nil || cfg.Duration != 0 || cfg.Iterations != 20 || cfg.VUs != 5 || cfg.P95 != 250*time.Millisecond || cfg.ErrorRate != rate {
		t.Fatalf("iteration override = %+v, %v", cfg, err)
	}
	scenario.Load.Iterations, scenario.Load.Duration = 10, ""
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Duration: "2s", DurationSet: true, VUs: 3, VUsSet: true})
	if err != nil || cfg.Iterations != 0 || cfg.Duration != 2*time.Second || cfg.VUs != 3 {
		t.Fatalf("duration override = %+v, %v", cfg, err)
	}
	for name, spec := range map[string]testLoadSpec{
		"vus": {VUs: 51}, "negative vus": {VUs: -1}, "iterations": {Iterations: 10001},
		"negative iterations": {Iterations: -1}, "both modes": {Iterations: 1, Duration: "1s"},
		"short duration": {Duration: "1ms"}, "long duration": {Duration: "6m"}, "bad duration": {Duration: "forever"},
		"zero p95": {Thresholds: testLoadThresholds{P95: "0s"}}, "long p95": {Thresholds: testLoadThresholds{P95: "2m"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateTestLoadSpec(&spec); err == nil {
				t.Fatal("invalid load spec accepted")
			}
		})
	}
	for _, rate := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		if err := validateTestLoadSpec(&testLoadSpec{Thresholds: testLoadThresholds{ErrorRate: &rate}}); err == nil {
			t.Fatalf("invalid error rate %v accepted", rate)
		}
	}
	for _, overrides := range []testLoadOverrides{
		{VUsSet: true}, {IterationsSet: true}, {DurationSet: true},
		{Iterations: 1, Duration: "1s", IterationsSet: true, DurationSet: true},
	} {
		if _, err := resolveTestLoadConfig(scenario, overrides); err == nil {
			t.Fatalf("invalid overrides accepted: %+v", overrides)
		}
	}
	scenario.Timeout = "1s"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{Duration: "2s", DurationSet: true}); err == nil {
		t.Fatal("duration exceeding scenario timeout accepted")
	}
	scenario.Timeout, scenario.Load = "", nil
	scenario.Requests = make([]testHTTPRequest, 100)
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{Iterations: 10000, IterationsSet: true}); err == nil {
		t.Fatal("HTTP-step budget overflow accepted")
	}
	if _, err := resolveTestLoadConfig(testScenario{Command: []string{"true"}}, testLoadOverrides{}); err == nil {
		t.Fatal("command-only load scenario accepted")
	}
}

func TestLoadConcurrentJourneysKeepCapturesAndIdentitiesIsolated(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	var arrivals, reads, forbidden atomic.Int32
	barrier := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST":
			if r.Header.Get("Authorization") != "Bearer private-key" {
				t.Error("consumer key was not passed to load journey")
			}
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			var body map[string]any
			if err := decoder.Decode(&body); err != nil {
				t.Error(err)
			}
			id, _ := body["id"].(string)
			if body["count"] != json.Number("9007199254740993") || body["enabled"] != true {
				t.Errorf("case types changed: %+v", body)
			}
			mu.Lock()
			if seen[id] || !strings.HasPrefix(id, "root-") {
				t.Errorf("iteration identity reused: %q", id)
			}
			seen[id] = true
			mu.Unlock()
			n := arrivals.Add(1)
			if n == 3 {
				close(barrier)
			}
			if n <= 3 {
				select {
				case <-barrier:
				case <-time.After(3 * time.Second):
					t.Error("three users did not execute concurrently")
				}
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "private/" + id})
		case strings.HasPrefix(r.URL.Path, "/read/"):
			reads.Add(1)
			if r.URL.Path != "/read/private/"+r.Header.Get("X-Run") || r.URL.Query().Get("customer") != "private/customer" {
				t.Errorf("capture crossed journeys: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": r.Header.Get("X-Run")})
		default:
			forbidden.Add(1)
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	steps := []testHTTPRequest{
		{Name: "submit", As: "customer", Method: "POST", Path: "/exports", JSON: map[string]any{"id": "${run.id}", "count": "${data.count}", "enabled": "${data.enabled}"}, Expect: testHTTPExpect{Status: 201}, Capture: map[string]string{"id": "/id"}},
		{Name: "read", Method: "GET", Path: "/read/${steps.submit.id}?customer=${data.customer}", Headers: map[string]string{"X-Run": "${run.id}"}, Expect: testHTTPExpect{Status: 200, JSON: map[string]any{"/id": "${run.id}"}}},
		{Name: "forbidden", Method: "GET", Path: "/forbidden", Expect: testHTTPExpect{Status: 403}},
	}
	cfg := &testLoadConfig{VUs: 3, Iterations: 9, RequestLimit: 100000}
	report, err := runTestLoad(context.Background(), server.URL, "root", []string{"GREGALE_TEST_CONSUMER_CUSTOMER_KEY=private-key"}, steps, map[string]any{"count": json.Number("9007199254740993"), "enabled": true, "customer": "private/customer"}, cfg)
	if err != nil || report.Status != "passed" || report.IterationsCompleted != 9 || report.IterationsStarted != 9 || report.PeakVUs != 3 || report.Requests != 27 || report.Failures != 0 || len(report.Steps) != 3 || report.Statuses["403"] != 9 {
		t.Fatalf("load report = %+v, %v", report, err)
	}
	if reads.Load() != 9 || forbidden.Load() != 9 || len(seen) != 9 {
		t.Fatalf("journeys incomplete: reads=%d forbidden=%d ids=%d", reads.Load(), forbidden.Load(), len(seen))
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"private-key", "private/customer", "private/root-", "9007199254740993"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("load report exposed %s", secret)
		}
	}
}

func TestLoadPercentilesThresholdsAndEmptySamples(t *testing.T) {
	samples := make([]time.Duration, 100)
	for i := range samples {
		samples[i] = time.Duration(100-i) * time.Millisecond
	}
	latency := testLoadLatencySummary(samples)
	if latency.MinMS != 1 || latency.MeanMS != 50.5 || latency.P50MS != 50 || latency.P95MS != 95 || latency.P99MS != 99 || latency.MaxMS != 100 || latency.Samples != 100 {
		t.Fatalf("nearest-rank percentiles = %+v", latency)
	}
	c := &testLoadCollector{started: 100, completed: 100, steps: []testLoadStepSamples{{evidence: testLoadStepEvidence{testLoadMetrics: testLoadMetrics{Requests: 100, Failures: 1, Statuses: map[string]int{"200": 99, "500": 1}}}, samples: samples}}}
	cfg := &testLoadConfig{VUs: 1, Iterations: 100, RequestLimit: 100000, ErrorRate: 0.01, P95: 95 * time.Millisecond}
	report := c.report(cfg, time.Second)
	if report.RequestsPerSecond != 100 || report.ErrorRate != 0.01 || !report.Thresholds[0].Passed || !report.Thresholds[1].Passed {
		t.Fatalf("inclusive thresholds = %+v", report)
	}
	cfg.P95--
	cfg.ErrorRate = 0
	report = c.report(cfg, time.Second)
	if report.Thresholds[0].Passed || report.Thresholds[1].Passed {
		t.Fatalf("failed thresholds passed: %+v", report.Thresholds)
	}
	report = (&testLoadCollector{}).report(cfg, 0)
	if report.Thresholds[0].Passed || report.Thresholds[1].Passed || report.Latency.Samples != 0 {
		t.Fatal("empty samples passed thresholds")
	}
}

func TestLoadFailuresRespectToleranceAndBoundErrorSamples(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1)%4 == 0 {
			w.WriteHeader(500)
		}
		_, _ = w.Write([]byte("private-response"))
	}))
	defer server.Close()
	cfg := &testLoadConfig{VUs: 2, Iterations: 12, RequestLimit: 100000, ErrorRate: 0.25}
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep(), loadTestStep()}, nil, cfg)
	if err != nil || report.Failures == 0 || report.ErrorRate > 0.25 || report.IterationsCompleted != 12 || report.IterationsFailed == 0 || len(report.Steps[0].Errors) > 1 || len(report.Steps[1].Errors) > 1 {
		t.Fatalf("tolerated failures = %+v, %v", report, err)
	}
	cfg.ErrorRate = 0
	report, err = runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.Status != "failed" || report.StopReason != "iterations" || report.Failures != 3 {
		t.Fatalf("default threshold did not fail: %+v, %v", report, err)
	}
	collector := &testLoadCollector{steps: make([]testLoadStepSamples, 1)}
	collector.steps[0].evidence.Statuses = map[string]int{}
	for i := range 10 {
		collector.record(0, testHTTPRequestEvidence{Error: fmt.Sprintf("error-%d", i)}, time.Millisecond)
	}
	if len(collector.steps[0].evidence.Errors) != 5 {
		t.Fatal("error examples not bounded")
	}
}

func TestLoadReadsFullBodiesReusesConnectionsAndRejectsOversize(t *testing.T) {
	var connections, redirects atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = w.Write(bytes.Repeat([]byte("x"), testHTTPBodyLimit+1))
		case "/redirect":
			http.Redirect(w, r, "/destination", http.StatusFound)
		case "/destination":
			redirects.Add(1)
		default:
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write([]byte("complete body"))
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	cfg := &testLoadConfig{VUs: 1, Iterations: 3, RequestLimit: 100000, P95: time.Nanosecond}
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.Failures != 0 || report.Latency.MinMS < 15 || report.Thresholds[1].Passed || connections.Load() != 1 {
		t.Fatalf("full-body timing/connection reuse = %+v, %v (%d connections)", report, err, connections.Load())
	}
	cfg.Iterations, cfg.P95 = 1, 0
	step := loadTestStep()
	step.Path = "/large"
	report, err = runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{step}, nil, cfg)
	if err == nil || report.Failures != 1 || !strings.Contains(report.Steps[0].Errors[0], "exceeds 1 MiB") {
		t.Fatalf("unbounded response accepted: %+v, %v", report, err)
	}
	step.Path, step.Expect.Status = "/redirect", 302
	report, err = runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{step}, nil, cfg)
	if err != nil || redirects.Load() != 0 || report.Statuses["302"] != 1 {
		t.Fatalf("redirect followed: %+v, %v", report, err)
	}
}

func TestLoadDurationBudgetAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	cfg := &testLoadConfig{VUs: 2, Duration: time.Second, RequestLimit: 100000}
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err != nil || report.Mode != "duration" || report.StopReason != "duration" || report.Requests == 0 || report.IterationsStarted != report.IterationsCompleted || report.DurationMS < 1000 || report.DurationMS > 4000 {
		t.Fatalf("duration run = %+v, %v", report, err)
	}
	cfg.Duration, cfg.RequestLimit = time.Second, 6
	report, err = runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.StopReason != "request_limit" || report.Requests != 6 || report.Status != "failed" {
		t.Fatalf("budget cap ignored: %+v, %v", report, err)
	}
	started := make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer blocked.Close()
	dir := t.TempDir()
	scenario := testScenario{Requests: []testHTTPRequest{loadTestStep()}, Cleanup: [][]string{{"sh", "-c", "printf cleanup > cleaned"}}}
	cfg = &testLoadConfig{VUs: 1, Iterations: 5, RequestLimit: 100000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	receipt := runLocalTestWithLoad(ctx, "canceled", scenario, dir, blocked.URL, testDataCase{}, cfg)
	if receipt.Status != "failed" || receipt.Load.StopReason != "canceled" || receipt.Load.IterationsInterrupted != 1 || receipt.Load.Statuses["0"] != 1 || receipt.CleanupError != "" {
		t.Fatalf("cancellation report = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(dir, "cleaned")); err != nil {
		t.Fatal("cleanup did not run after canceled load", err)
	}
}

func TestLoadCLIReportsCasesHooksAndFailureExit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_TOKEN", "account-secret")
	t.Setenv("GREGALE_TEST_LOAD_JSON", "stale-data")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("customer") == "private/b" {
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	contents := `version: 1
scenarios:
  api-load:
    project: my-api
    load: {vus: 2, iterations: 4, thresholds: {p95: 1s}}
    setup:
      - [sh, -c, 'test -z "$FAAS_TOKEN" && test -z "$GREGALE_TEST_LOAD_JSON" && test "$GREGALE_TEST_LOAD" = 1 && test "$GREGALE_TEST_LOAD_VUS" = 2 && printf "setup:%s\n" "$GREGALE_TEST_CASE" >> hooks']
    trigger: [sh, -c, 'printf "trigger:%s\n" "$GREGALE_TEST_CASE" >> hooks']
    requests:
      - name: ping
        method: GET
        path: /ping?customer=${data.customer}
        expect: {status: 200}
    command: [sh, -c, 'test -n "$GREGALE_TEST_LOAD_JSON" && printf "assert:%s\n" "$GREGALE_TEST_CASE" >> hooks; echo command-output']
    cleanup:
      - [sh, -c, 'test -n "$GREGALE_TEST_LOAD_JSON" && printf "cleanup:%s\n" "$GREGALE_TEST_CASE" >> hooks']
`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(data, []byte(`[{"customer":"private/a"},{"customer":"private/b"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	var stdout, stderr bytes.Buffer
	jsonOutput, osStdout, osStderr = true, &stdout, &stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	args := []string{"--scenario", "api-load", "--engine", "local", "--load", "--manifest", manifest, "--data", data}
	if code := cmdTest(append(append([]string{}, args...), "--validate")); code != 0 || requests.Load() != 0 {
		t.Fatalf("load validation contacted application: %d %s", code, stderr.String())
	}
	stdout.Reset()
	reportPath, junitPath := filepath.Join(dir, "results.json"), filepath.Join(dir, "results.xml")
	args = append(args, "--base-url", server.URL, "--repeat", "2", "--report", reportPath, "--junit", junitPath)
	if code := cmdTest(args); code != 1 {
		t.Fatalf("failed threshold exited %d: %s", code, stdout.String())
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 4 || requests.Load() != 16 {
		t.Fatalf("load JSON/cases = %s (%v), requests=%d", stdout.String(), err, requests.Load())
	}
	for i, receipt := range receipts {
		if receipt.Load == nil || receipt.Load.Requests != 4 || len(receipt.Requests) != 0 || receipt.Case != fmt.Sprintf("row-%d", i%2+1) || receipt.Attempt != i/2+1 || (receipt.Status == "passed") != (i%2 == 0) {
			t.Fatalf("case receipt %d = %+v", i, receipt)
		}
		if len(receipt.Phases) < 4 || receipt.Phases[2].Name != "load" || receipt.Phases[len(receipt.Phases)-1].Name != "cleanup" {
			t.Fatalf("missing load/cleanup phase: %+v", receipt.Phases)
		}
	}
	hooks, err := os.ReadFile(filepath.Join(dir, "hooks"))
	wantHooks := strings.Repeat("setup:row-1\ntrigger:row-1\nassert:row-1\ncleanup:row-1\nsetup:row-2\ntrigger:row-2\ncleanup:row-2\n", 2)
	if err != nil || string(hooks) != wantHooks {
		t.Fatalf("hooks did not run once per case: %q (%v)", hooks, err)
	}
	for _, path := range []string{reportPath, junitPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private/a", "private/b", "account-secret", "stale-data"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatalf("report exposed %s", secret)
			}
		}
		if path == junitPath {
			var suite testJUnitSuite
			if err := xml.Unmarshal(body, &suite); err != nil || suite.Tests != 4 || suite.Failures != 2 || suite.Cases[0].Name != "api-load/local/load/row-1#1" {
				t.Fatalf("JUnit load cases = %+v, %v", suite, err)
			}
		}
	}
}

func TestLoadCLIRejectsInvalidOptionsAndManifestDoesNotEnableLoad(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, []byte("version: 1\nscenarios:\n  api-smoke:\n    project: my-api\n    load: {vus: 2, iterations: 4}\n    requests:\n      - {name: ping, method: GET, path: /ping, expect: {status: 200}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var stdout, stderr bytes.Buffer
	osStdout, osStderr, jsonOutput = &stdout, &stderr, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	for _, options := range [][]string{
		{"--vus", "2"}, {"--iterations", "2"}, {"--duration", "1s"},
		{"--load", "--engine", "simulated"}, {"--load", "--engine", "real-vm"},
		{"--load", "--vus", "0"}, {"--load", "--iterations", "0"},
		{"--load", "--iterations", "2", "--duration", "1s"}, {"--load", "--duration", "6m"},
	} {
		args := append([]string{"--validate", "--engine", "local", "--manifest", manifest}, options...)
		if code := cmdTest(args); code == 0 {
			t.Fatalf("invalid load options accepted: %v", options)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	stdout.Reset()
	jsonOutput = true
	if code := cmdTest([]string{"--engine", "local", "--scenario", "api-smoke", "--base-url", server.URL, "--manifest", manifest}); code != 0 {
		t.Fatal(stderr.String())
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || requests.Load() != 1 || len(receipts) != 1 || receipts[0].Load != nil {
		t.Fatalf("manifest enabled load without flag: %s (%v), requests=%d", stdout.String(), err, requests.Load())
	}
	bad := strings.ReplaceAll("version: 1\nscenarios:\n  api-smoke:\n    project: my-api\n    load: {vus: 51}\n    command: [true]\n", "[true]", "['true']")
	if err := os.WriteFile(manifest, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(manifest); err == nil {
		t.Fatal("invalid load manifest accepted")
	}
}
