package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func circuitTestSpec() *testCircuitBreakerSpec {
	return &testCircuitBreakerSpec{CounterPath: "/test/cache-attempts", Request: "incoming", MinRequests: 5, StopWithin: "2s", Window: "1s", MaxProbes: 0, ResumeWithin: "2s", ResumeMinAttempts: 3}
}

func circuitTestEvidence(mode string) (*testLoadPhaseClock, []testLoadChaosPhaseEvidence, testCircuitObservation) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(2*time.Second))
	clock.transition(2, start.Add(6*time.Second))
	clock.finish(start.Add(10 * time.Second))
	phases := []testLoadChaosPhaseEvidence{{Name: "baseline"}, {Name: "fault"}, {Name: "recovery"}}
	observation := testCircuitObservation{}
	count := int64(0)
	for i := range phases {
		phases[i].Steps = []testLoadStepEvidence{{Name: "incoming", testLoadMetrics: testLoadMetrics{Requests: 20}}}
		boundary := clock.window(i)
		for j := 1; j < int(boundary.FinishedAt.Sub(boundary.StartedAt)/(100*time.Millisecond)); j++ {
			active := i != 1
			if mode == "no-open" && i == 1 {
				active = true
			}
			if i == 1 && (mode == "single-probe" || mode == "probe-burst") && (j == 20 || (mode == "probe-burst" && j == 22)) {
				active = true
			}
			if mode == "probes" && i == 1 && j >= 25 {
				active = true
			}
			if mode == "slow-resume" && i == 2 && j < 25 {
				active = false
			}
			if mode == "unstable-resume" && i == 2 && j >= 21 {
				active = false
			}
			if mode == "idle-baseline" && i == 0 {
				active = false
			}
			phases[i].CircuitRequests = append(phases[i].CircuitRequests, testCircuitIncomingSample{At: boundary.StartedAt.Add(time.Duration(j) * 100 * time.Millisecond), Name: "incoming"})
			if active {
				count++
			}
			observation.Samples = append(observation.Samples, testCircuitSample{At: boundary.StartedAt.Add(time.Duration(j) * 100 * time.Millisecond), Phase: i, Count: count})
		}
	}
	return clock, phases, observation
}

func TestCircuitBreakerActivityWindows(t *testing.T) {
	for _, mode := range []string{"healthy", "no-open", "probes", "slow-resume", "unstable-resume", "idle-baseline", "counter-reset", "sampling-gap", "empty-load", "interrupted", "paused-load", "single-probe", "probe-burst", "transition-overhead"} {
		t.Run(mode, func(t *testing.T) {
			clock, phases, observation := circuitTestEvidence(mode)
			switch mode {
			case "transition-overhead":
				shift := 1010 * time.Millisecond
				clock.windows[1].FinishedAt = clock.windows[1].FinishedAt.Add(-shift)
				clock.windows[2].StartedAt = clock.windows[2].StartedAt.Add(-shift)
				clock.windows[2].FinishedAt = clock.windows[2].FinishedAt.Add(-shift)
				var adjusted []testCircuitSample
				for _, sample := range observation.Samples {
					if sample.Phase == 1 && !sample.At.Before(clock.windows[1].FinishedAt) {
						continue
					}
					if sample.Phase == 2 {
						sample.At = sample.At.Add(-shift)
					}
					adjusted = append(adjusted, sample)
				}
				observation.Samples = adjusted
				var incoming []testCircuitIncomingSample
				for _, sample := range phases[1].CircuitRequests {
					if sample.At.Before(clock.windows[1].FinishedAt) {
						incoming = append(incoming, sample)
					}
				}
				phases[1].CircuitRequests = incoming
				for j := range phases[2].CircuitRequests {
					phases[2].CircuitRequests[j].At = phases[2].CircuitRequests[j].At.Add(-shift)
				}
			case "counter-reset":
				observation.Samples[30].Count = 0
			case "sampling-gap":
				observation.Samples = append(observation.Samples[:25], observation.Samples[35:]...)
			case "empty-load":
				phases[1].Steps = nil
			case "paused-load":
				phases[1].CircuitRequests = nil
			case "interrupted":
				clock.windows[2].FinishedAt = time.Time{}
			}
			spec := circuitTestSpec()
			if mode == "single-probe" || mode == "probe-burst" {
				spec.MaxProbes = 1
			}
			evidence := evaluateTestCircuitBreaker(spec, phases, 1, clock, observation)
			if evidence.Passed != (mode == "healthy" || mode == "single-probe" || mode == "transition-overhead") {
				t.Fatalf("unexpected evidence: %+v", evidence)
			}
			if mode == "healthy" && (evidence.StoppedMS == nil || evidence.ResumedMS == nil || *evidence.StoppedMS > 2000 || *evidence.ResumedMS > 2000) {
				t.Fatal("confirmed timing missing")
			}
			if mode != "healthy" && mode != "single-probe" && mode != "transition-overhead" && evidence.Error == "" {
				t.Fatal("failure reason missing")
			}
		})
	}
}

func TestCircuitBreakerValidation(t *testing.T) {
	request := loadTestStep()
	spec := circuitTestSpec()
	spec.Request = request.Name
	phases := []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "2s"}, {Name: "fault", Duration: "4s", Rules: []testChaosRule{{From: "app", To: "cache", Kind: "http_status", Percent: 100, StatusCode: 503}}, CircuitBreaker: spec}, {Name: "recovery", Duration: "4s"}}
	scenario := testScenario{Requests: []testHTTPRequest{request}, Load: &testLoadSpec{Duration: "10s", ChaosSchedule: phases}}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*testCircuitBreakerSpec){
		func(s *testCircuitBreakerSpec) { s.Window = "0s" },
		func(s *testCircuitBreakerSpec) { s.StopWithin = "1s" },
		func(s *testCircuitBreakerSpec) { s.ResumeWithin = "1s" },
		func(s *testCircuitBreakerSpec) { s.MaxProbes = -1 },
		func(s *testCircuitBreakerSpec) { s.ResumeMinAttempts = 0 },
		func(s *testCircuitBreakerSpec) { s.CounterPath = "https://example.com/counter" },
		func(s *testCircuitBreakerSpec) { s.CounterPath = "//example.com/counter" },
		func(s *testCircuitBreakerSpec) { s.CounterPath = "/${secret}" },
		func(s *testCircuitBreakerSpec) { s.Request = "missing" },
	} {
		copied := *spec
		mutate(&copied)
		scenario.Load.ChaosSchedule[1].CircuitBreaker = &copied
		if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
			t.Fatalf("invalid spec accepted: %+v", copied)
		}
	}
}

func TestCircuitBreakerCounterReader(t *testing.T) {
	for _, body := range []string{`{"attempts":12}`, `{"attempts":-1}`, `{"attempts":1.5}`, `{"attempts":"3"}`, `{}`, `{"attempts":3,"token":"hidden"}`, `{"attempts":3} {}`, strings.Repeat("x", 4097)} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing consumer credential")
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			spec := circuitTestSpec()
			spec.As = "tester"
			count, err := readTestCircuitCounter(context.Background(), server.Client(), server.URL, spec, map[string]string{"tester": "secret"})
			if body == `{"attempts":12}` {
				if err != nil || count != 12 {
					t.Fatalf("count=%d err=%v", count, err)
				}
			} else if err == nil {
				t.Fatal("invalid counter accepted")
			}
			if err != nil && (strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "secret")) {
				t.Fatal("response or credential leaked")
			}
		})
	}
}

func TestCircuitBreakerReports(t *testing.T) {
	clock, phases, observation := circuitTestEvidence("probes")
	cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{}, {Name: "fault", Rules: []testChaosRule{{From: "app", To: "cache"}}, CircuitBreaker: circuitTestSpec()}, {}}}
	report := &testLoadEvidence{Status: "passed", ChaosPhases: phases}
	failure := applyTestCircuitBreakers(report, cfg, clock, []testCircuitObservation{{}, observation, {}}, nil)
	if failure == nil || report.Status != "failed" {
		t.Fatal("probe storm did not fail load")
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "circuit breaker app -> cache") || !strings.Contains(terminal.String(), "probe budget exceeded") {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "breaker", Status: "failed", Error: failure.Error(), Load: report}
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 {
		t.Fatalf("JUnit failure missing: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.Load.ChaosPhases[1].CircuitBreaker.StoppedMS == nil {
		t.Fatalf("JSON timing missing: %v", err)
	}
	path = filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Circuit breaker app") || !strings.Contains(string(body), "probe budget exceeded") {
		t.Fatal("HTML breaker evidence missing")
	}
}

func TestCircuitBreakerScheduledRuntime(t *testing.T) {
	var fault atomic.Bool
	var attempts atomic.Int64
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			fault.Store(true)
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "breaker-generation", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
		case http.MethodDelete:
			fault.Store(false)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "breaker-generation"})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer control.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test/cache-attempts" {
			_ = json.NewEncoder(w).Encode(map[string]int64{"attempts": attempts.Load()})
			return
		}
		if !fault.Load() {
			attempts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	request := loadTestStep()
	request.Name = "incoming"
	loadSpec := &testLoadSpec{Duration: "10s", VUs: 2, Rate: 40, ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "2s"}, {Name: "fault", Duration: "4s", Rules: []testChaosRule{{From: "app", To: "cache", Kind: "http_status", Percent: 100, StatusCode: 503}}, CircuitBreaker: circuitTestSpec()}, {Name: "recovery", Duration: "4s"}}}
	cfg, err := resolveTestLoadConfig(testScenario{Requests: []testHTTPRequest{request}, Load: loadSpec}, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report, err := runTestScenarioLoadOnce(ctx, NewClient(control.URL, "token"), app.URL, "run", nil, []testHTTPRequest{request}, nil, cfg)
	if err != nil {
		t.Fatalf("scheduled circuit test: %v", err)
	}
	evidence := report.ChaosPhases[1].CircuitBreaker
	if evidence == nil || !evidence.Passed || evidence.StoppedMS == nil || evidence.ResumedMS == nil {
		t.Fatalf("runtime observations missing: %+v", evidence)
	}
	if fault.Load() {
		t.Fatal("fault not cleared")
	}
}

func TestCircuitBreakerMonitorRejectsCounterReset(t *testing.T) {
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := int64(10)
		if reads.Add(1) > 1 {
			count = 0
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"attempts": count})
	}))
	defer server.Close()
	cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{}, {CircuitBreaker: circuitTestSpec()}, {}}}
	clock := newTestLoadPhaseClock(3)
	clock.start(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observation := monitorTestCircuitCounter(ctx, server.URL, nil, cfg, clock, 1)
	if !strings.Contains(observation.Error, "reset or decreased") {
		t.Fatalf("counter reset accepted: %+v", observation)
	}
}
