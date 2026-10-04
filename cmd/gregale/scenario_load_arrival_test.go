package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"math"
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

func TestLoadArrivalConfigurationBudgetsAndOverrides(t *testing.T) {
	scenario := testScenario{Requests: []testHTTPRequest{loadTestStep()}, Load: &testLoadSpec{Rate: 20, Duration: "1500ms", VUs: 5}}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil || cfg.Rate != 20 || cfg.Iterations != 0 || cfg.plannedArrivals() != 30 || cfg.maxVUs() != 5 {
		t.Fatalf("arrival configuration = %+v (%v)", cfg, err)
	}
	metadata := newTestLoadEvidence(cfg)
	if metadata.Mode != "arrival-rate" || metadata.Arrival == nil || metadata.Arrival.TargetRate != 20 || metadata.Arrival.Planned != 30 || metadata.Status != "not_started" {
		t.Fatalf("arrival metadata = %+v", metadata)
	}
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Iterations: 10, IterationsSet: true})
	if err != nil || cfg.Rate != 0 || cfg.Duration != 0 || cfg.Iterations != 10 || newTestLoadEvidence(cfg).Arrival != nil {
		t.Fatalf("iteration override did not clear arrival model: %+v (%v)", cfg, err)
	}
	scenario.Load = &testLoadSpec{VUs: 3, Pacing: "100ms", Stages: []testLoadStageSpec{loadStage("1s", 5)}}
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Rate: 7, RateSet: true, Duration: "1500ms", DurationSet: true})
	if err != nil || cfg.Rate != 7 || cfg.plannedArrivals() != 11 || cfg.Iterations != 0 || len(cfg.Stages) != 0 || cfg.Pacing != 0 {
		t.Fatalf("arrival override did not clear concurrency schedule: %+v (%v)", cfg, err)
	}
	for _, overrides := range []testLoadOverrides{
		{RateSet: true}, {Rate: -1, RateSet: true}, {Rate: 1001, RateSet: true, Duration: "1s", DurationSet: true},
		{Rate: 1, RateSet: true}, {Rate: 1, RateSet: true, Iterations: 2, IterationsSet: true},
		{Rate: 1, RateSet: true, Duration: "1s", DurationSet: true, Pacing: "1ms", PacingSet: true},
	} {
		if _, err := resolveTestLoadConfig(scenario, overrides); err == nil {
			t.Fatalf("invalid arrival overrides accepted: %+v", overrides)
		}
	}
	for _, spec := range []testLoadSpec{
		{Rate: -1}, {Rate: 1001, Duration: "1s"}, {Rate: 1}, {Rate: 1, Duration: "1ms"},
		{Rate: 1, Duration: "1s", Iterations: 1}, {Rate: 1, Duration: "1s", Stages: []testLoadStageSpec{loadStage("1s", 1)}},
		{Rate: 1, Duration: "1s", Pacing: "1ms"}, {Rate: 1, Duration: "1s", Pacing: "later"},
	} {
		if err := validateTestLoadSpec(&spec); err == nil {
			t.Fatalf("invalid arrival specification accepted: %+v", spec)
		}
	}
	if err := validateTestLoadSpec(&testLoadSpec{Rate: 1, Duration: "1s", Pacing: "0s"}); err != nil {
		t.Fatalf("explicit zero pacing rejected: %v", err)
	}
	scenario.Load = &testLoadSpec{Rate: 1000, Duration: "100s"}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err != nil {
		t.Fatalf("inclusive HTTP-step budget rejected: %v", err)
	}
	scenario.Load.Duration = "100001ms"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil || !strings.Contains(err.Error(), "HTTP steps") {
		t.Fatalf("fractional arrival exceeded request guard: %v", err)
	}
	scenario.Load.Duration, scenario.Timeout = "2s", "1s"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
		t.Fatal("arrival duration exceeded scenario timeout")
	}
}

func TestLoadArrivalDeadlinesDropBacklogWithoutCatchUp(t *testing.T) {
	schedule := testLoadArrivalSchedule{rate: 3, duration: time.Second, total: 3}
	for _, tc := range []struct {
		elapsed          time.Duration
		arrival, dropped int
		delay            time.Duration
		done             bool
	}{
		{0, 1, 0, 0, false},
		{333333333 * time.Nanosecond, 0, 0, time.Nanosecond, false},
		{800 * time.Millisecond, 3, 1, 0, false},
		{800 * time.Millisecond, 0, 0, 200 * time.Millisecond, false},
		{time.Second, 0, 0, 0, true},
	} {
		arrival, dropped, delay, done := schedule.due(tc.elapsed)
		if arrival != tc.arrival || dropped != tc.dropped || delay != tc.delay || done != tc.done {
			t.Fatalf("deadline at %s = (%d, %d, %s, %t), want %+v", tc.elapsed, arrival, dropped, delay, done, tc)
		}
	}
	boundary := testLoadArrivalSchedule{rate: 3, duration: time.Second, total: 3}
	_, _, _, _ = boundary.due(0)
	arrival, dropped, _, _ := boundary.due(333333334 * time.Nanosecond)
	if arrival != 2 || dropped != 0 {
		t.Fatalf("non-divisor rate repeated an identity: %d, %d", arrival, dropped)
	}
	late := testLoadArrivalSchedule{rate: 10, duration: time.Second, total: 10}
	arrival, dropped, _, done := late.due(time.Second)
	if arrival != 0 || dropped != 10 || !done {
		t.Fatalf("expired schedule started late work: %d, %d, %t", arrival, dropped, done)
	}
	if _, dropped, _, _ := late.due(2 * time.Second); dropped != 0 {
		t.Fatal("late arrivals counted twice")
	}
}

func TestLoadArrivalStartsWhileResponsesAreHeldAndDrainsWholeJourneys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	var mu sync.Mutex
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			if r.Header.Get("Authorization") != "Bearer private-key" {
				t.Error("arrival journey lost consumer authentication")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			id, _ := body["id"].(string)
			mu.Lock()
			if seen[id] || id == "" {
				t.Errorf("arrival identity empty or reused: %q", id)
			}
			seen[id] = true
			mu.Unlock()
			if requests.Add(1) == 3 {
				close(started)
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "private/" + id})
			return
		}
		if r.URL.Path != "/read/private/"+r.Header.Get("X-Run") || r.Header.Get("X-Customer") != "private-customer" {
			t.Errorf("arrival captures/data crossed journeys: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"run": r.Header.Get("X-Run")})
	}))
	defer server.Close()
	go func() {
		select {
		case <-started:
			_ = waitTestLoad(ctx, 100*time.Millisecond)
		case <-ctx.Done():
		}
		close(release)
	}()
	steps := []testHTTPRequest{
		{Name: "submit", As: "buyer", Method: "POST", Path: "/submit", JSON: map[string]any{"id": "${run.id}"}, Expect: testHTTPExpect{Status: 200}, Capture: map[string]string{"id": "/id"}},
		{Name: "read", Method: "GET", Path: "/read/${steps.submit.id}", Headers: map[string]string{"X-Run": "${run.id}", "X-Customer": "${data.customer}"}, Expect: testHTTPExpect{Status: 200, JSON: map[string]any{"/run": "${run.id}"}}},
	}
	cfg := &testLoadConfig{Rate: 20, Duration: 150 * time.Millisecond, VUs: 3, RequestLimit: 100}
	report, err := runTestLoad(ctx, server.URL, "root", []string{"GREGALE_TEST_CONSUMER_BUYER_KEY=private-key"}, steps, map[string]any{"customer": "private-customer"}, cfg)
	if err != nil || report.Mode != "arrival-rate" || report.Status != "passed" || report.IterationsStarted != 3 || report.IterationsCompleted != 3 || report.Requests != 6 || report.PeakVUs != 3 || report.DurationMS < 190 || report.Arrival.Scheduled != 3 || report.Arrival.Planned != 3 || report.Arrival.Dropped != 0 || math.Abs(report.Arrival.StartedPerSecond-20) > 2 {
		t.Fatalf("independent arrivals/drain = %+v, arrival=%+v (%v)", report, report.Arrival, err)
	}
	body, _ := json.Marshal(report)
	for _, secret := range []string{"private-key", "private-customer", "private/root-"} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatalf("arrival report leaked %s", secret)
		}
	}
}

func TestLoadArrivalDropsAtConcurrencyCapEvenWithSuccessfulHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = waitTestLoad(r.Context(), 450*time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg := &testLoadConfig{Rate: 10, Duration: 350 * time.Millisecond, VUs: 1, RequestLimit: 100, ErrorRate: 1}
	report, err := runTestLoad(ctx, server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.Status != "failed" || report.StopReason != "dropped_arrivals" || report.PeakVUs != 1 || requests.Load() != 1 || report.IterationsCompleted != 1 || report.Failures != 0 || !testLoadThresholdsPassed(report) || report.Arrival.Scheduled != 4 || report.Arrival.Dropped != 3 || report.Arrival.DroppedCapacity != 3 || report.Arrival.DroppedLate != 0 {
		t.Fatalf("missing traffic incorrectly passed: %+v, arrival=%+v (%v)", report, report.Arrival, err)
	}
	var human bytes.Buffer
	printTestLoadSummary(&human, report)
	if !strings.Contains(human.String(), "3 dropped") || !strings.Contains(human.String(), "target 10 journeys/s") {
		t.Fatalf("drops missing from terminal summary: %s", &human)
	}
}

func TestLoadArrivalCancellationAndRequestBudgetBoundWork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	cfg := &testLoadConfig{Rate: 10, Duration: time.Second, VUs: 1, RequestLimit: 2}
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.StopReason != "request_limit" || report.Requests != 2 || requests.Load() != 2 || report.Arrival.Scheduled >= report.Arrival.Planned || report.IterationsInterrupted == 0 {
		t.Fatalf("arrival budget exceeded: %+v (%v)", report, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = runTestLoad(ctx, server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.StopReason != "canceled" || report.Arrival.Scheduled != 0 || report.Requests != 0 || requests.Load() != 2 {
		t.Fatalf("canceled arrivals scheduled work: %+v (%v)", report, err)
	}
}

func TestLoadArrivalCancellationStopsScheduledAndInFlightWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg := &testLoadConfig{Rate: 1, Duration: 5 * time.Second, VUs: 2, RequestLimit: 100}
	report, err := runTestLoad(ctx, server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.StopReason != "canceled" || requests.Load() != 1 || report.IterationsStarted != 1 || report.IterationsInterrupted != 1 || report.Arrival.Scheduled != 1 || report.Arrival.Planned != 5 || report.DurationMS > 1000 {
		t.Fatalf("cancellation did not stop arrivals/requests: %+v (%v)", report, err)
	}
}

func TestLoadArrivalDroppedWorkFailsCLIAndJUnitAfterCleanup(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = waitTestLoad(r.Context(), 1200*time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()
	scenario := suiteTestScenario("/ping")
	scenario.Load = &testLoadSpec{Rate: 10, Duration: "1s", VUs: 1}
	scenario.Cleanup = [][]string{{"sh", "-c", "echo cleaned > cleanup.log"}}
	manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"api-load": scenario}})
	junitPath := filepath.Join(dir, "load.xml")
	if code := cmdTest([]string{"--scenario", "api-load", "--manifest", manifest, "--engine", "local", "--load", "--base-url", server.URL, "--junit", junitPath}); code != 1 {
		t.Fatalf("dropped traffic exited %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 || receipts[0].Status != "failed" || receipts[0].Load.Arrival.Dropped == 0 || receipts[0].Load.Failures != 0 {
		t.Fatalf("dropped traffic receipt = %s (%v)", stdout, err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "cleanup.log")); err != nil || string(body) != "cleaned\n" {
		t.Fatalf("cleanup after drops = %s (%v)", body, err)
	}
	body, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatal(err)
	}
	var junit testJUnitSuite
	if err := xml.Unmarshal(body, &junit); err != nil || junit.Failures != 1 || junit.Cases[0].Failure == nil || !strings.Contains(junit.Cases[0].Failure.Message, "dropped") || !strings.Contains(junit.Cases[0].SystemOut, "dropped_capacity") {
		t.Fatalf("JUnit did not fail on dropped arrivals: %+v (%v)", junit, err)
	}
}

func TestLoadArrivalSuiteCLIReportsDataHooksProgressAndJUnit(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	t.Setenv("FAAS_TOKEN", "private-account-key")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	scenario := suiteTestScenario("/ping")
	scenario.Load = &testLoadSpec{VUs: 1, Iterations: 2, Pacing: "100ms"}
	scenario.Checks[0].Headers = map[string]string{"X-Customer": "${data.customer}"}
	scenario.Setup = [][]string{{"sh", "-c", `test -z "$FAAS_TOKEN" && test "$GREGALE_TEST_LOAD_MODE" = arrival-rate && test "$GREGALE_TEST_LOAD_RATE" = 4 && test "$GREGALE_TEST_LOAD_MAX_VUS" = 2`}}
	scenario.Cleanup = [][]string{{"sh", "-c", `test -n "$GREGALE_TEST_LOAD_JSON" && printf 'cleaned:%s\n' "$GREGALE_TEST_CASE" >> hooks`}}
	writeSuiteTestFile(t, filepath.Join(dir, "cases.json"), `[{"customer":"private-first"},{"customer":"private-second"}]`)
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"api-load": scenario},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "api-load", Engine: "local", Data: "cases.json"}}}},
	})
	args := []string{"--suite", "smoke", "--manifest", manifest, "--load", "--rate", "4", "--duration", "2s", "--vus", "2"}
	if code := cmdTest(append(append([]string{}, args...), "--validate")); code != 0 || requests.Load() != 0 {
		t.Fatalf("arrival validation contacted application: exit %d, %s", code, stderr)
	}
	stdout.Reset()
	reportPath, junitPath := filepath.Join(dir, "load.json"), filepath.Join(dir, "load.xml")
	if code := cmdTest(append(args, "--base-url", server.URL, "--progress", "--report", reportPath, "--junit", junitPath)); code != 0 {
		t.Fatalf("arrival suite exit %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 || requests.Load() != 16 {
		t.Fatalf("arrival reports = %s (%v), requests=%d", stdout, err, requests.Load())
	}
	for _, receipt := range receipts {
		if receipt.Suite != "smoke" || receipt.Status != "passed" || receipt.Load.Arrival.Planned != 8 || receipt.Load.Arrival.Scheduled != 8 || receipt.Load.Arrival.Dropped != 0 || receipt.Load.IterationsCompleted != 8 {
			t.Fatalf("arrival suite receipt = %+v", receipt)
		}
	}
	if !strings.Contains(stderr.String(), "target 4 journeys/s") || strings.Contains(stdout.String(), "journeys completed") {
		t.Fatalf("progress misplaced: stdout=%s stderr=%s", stdout, stderr)
	}
	hooks, err := os.ReadFile(filepath.Join(dir, "hooks"))
	if err != nil || string(hooks) != "cleaned:row-1\ncleaned:row-2\n" {
		t.Fatalf("arrival cleanup missing: %s (%v)", hooks, err)
	}
	body, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatal(err)
	}
	var junit testJUnitSuite
	if err := xml.Unmarshal(body, &junit); err != nil || junit.Tests != 2 || junit.Failures != 0 || !strings.Contains(junit.Cases[0].SystemOut, "target_journeys_per_second") {
		t.Fatalf("arrival JUnit evidence = %+v (%v)", junit, err)
	}
	for _, path := range []string{reportPath, junitPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("private-")) {
			t.Fatalf("arrival report leaked inputs: %s", path)
		}
	}
	for _, extra := range [][]string{{"--rate", "0"}, {"--rate", "1001"}, {"--rate", "4", "--iterations", "2"}, {"--rate", "4", "--pacing", "1ms"}} {
		invalid := append([]string{"--suite", "smoke", "--manifest", manifest, "--load", "--duration", "1s"}, extra...)
		if code := cmdTest(invalid); code == 0 {
			t.Fatalf("invalid arrival options accepted: %v", extra)
		}
	}
	if code := cmdTest([]string{"--suite", "smoke", "--manifest", manifest, "--rate", "4"}); code == 0 {
		t.Fatal("rate enabled without --load")
	}
}
