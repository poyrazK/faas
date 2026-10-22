package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func strictAttemptFixture(values []int64) (testLoadChaosPhaseEvidence, chaos.Rule) {
	rule := chaos.Rule{From: "app", To: "dependency", Kind: chaos.KindHTTPStatus, StatusCode: 503, Percent: 100, CorrelationScope: strings.Repeat("b", 16)}
	counts := map[string]int64{}
	var total int64
	phase := testLoadChaosPhaseEvidence{Chaos: &testChaosEvidence{Matches: []testChaosRuleEvidence{{Rule: rule}}, CorrelationCounts: counts}}
	for i, count := range values {
		expected := count
		phase.CorrelationRequests = append(phase.CorrelationRequests, testRequestAttemptSample{ID: chaos.RequestCorrelationID(strings.Repeat("a", 32), rule.CorrelationScope, i+1), Expected: &expected})
		counts[chaos.CorrelationCounterID(rule, i+1)] = count
		total += count
	}
	counts[chaos.CorrelationSupportID(rule)] = total
	phase.Chaos.Matches[0].Matches = total
	return phase, rule
}

func TestPerRequestAttemptsRejectMaskedOutlierAndIncompleteCoverage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int64
		alter  func(*testLoadChaosPhaseEvidence, chaos.Rule)
		pass   bool
	}{
		{name: "boundary", values: []int64{2, 2, 2}, pass: true},
		{name: "zero acknowledged", values: []int64{0, 1, 2}, pass: true},
		{name: "average masks outlier", values: []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 11}},
		{name: "missing receipt", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) { p.CorrelationRequests[0].Expected = nil }},
		{name: "late record", values: []int64{1, 2}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.Chaos.CorrelationCounts[chaos.CorrelationCounterID(r, 2)] = 1
		}},
		{name: "unpropagated", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.Chaos.CorrelationCounts[chaos.UncorrelatedAttemptID(r)] = 1
		}},
		{name: "unsupported", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			delete(p.Chaos.CorrelationCounts, chaos.CorrelationSupportID(r))
		}},
		{name: "stale phase", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.CorrelationRequests[0].ID = chaos.RequestCorrelationID(strings.Repeat("a", 32), strings.Repeat("c", 16), 1)
		}},
		{name: "foreign namespace", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.CorrelationRequests[1].ID = chaos.RequestCorrelationID(strings.Repeat("c", 32), r.CorrelationScope, 2)
		}},
		{name: "duplicate ID", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.CorrelationRequests[1].ID = p.CorrelationRequests[0].ID
		}},
		{name: "overflow", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) { p.CorrelationOverflow = 1 }},
		{name: "extra traffic", values: []int64{1, 1}, alter: func(p *testLoadChaosPhaseEvidence, r chaos.Rule) {
			p.Chaos.CorrelationCounts[chaos.CorrelationSupportID(r)]++
			p.Chaos.Matches[0].Matches++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phase, rule := strictAttemptFixture(tc.values)
			if tc.alter != nil {
				tc.alter(&phase, rule)
			}
			e := evaluateTestPerRequestAttempts(phase, rule, 2, len(tc.values))
			if e.Passed != tc.pass {
				t.Fatalf("%+v", e)
			}
			if tc.name == "average masks outlier" && (e.ViolationCount != 1 || e.MaxAttempts != 11 || len(e.Violations) != 1) {
				t.Fatalf("masked outlier lost: %+v", e)
			}
		})
	}
}

func TestPerRequestAttemptsValidationAndCapacity(t *testing.T) {
	body := strings.Replace(attemptAssertionManifest(false), "max_attempts_per_request: 2", "per_request: true, max_attempts_per_request: 2", 1)
	if _, err := readAssertionProfileManifest(t, body); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Replace(body, "max_attempts_per_request: 2", "max_attempts_per_request: 2.5", 1), strings.Replace(body, "min_requests: 3", "min_requests: 1025", 1), strings.Replace(body, "path: /payment, expect:", "path: /payment, headers: {X-Gregale-Test-Request-ID: override}, expect:", 1), strings.Replace(attemptAssertionManifest(true), "max_attempts_per_request: 2", "per_request: true, max_attempts_per_request: 2", 1)} {
		if _, err := readAssertionProfileManifest(t, invalid); err == nil {
			t.Fatal("invalid strict configuration accepted")
		}
	}
	for _, value := range []string{"", "-1", "01", "1.0", "1048577"} {
		h := http.Header{}
		h.Set(testRequestAttemptsHeader, value)
		if readTestAttemptReceipt(h) != nil {
			t.Fatal("invalid receipt accepted")
		}
	}
	cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{Rules: []testChaosRule{{CorrelationScope: strings.Repeat("b", 16)}}, RetryAmplification: &testRetryAmplificationSpec{Request: "read", PerRequest: true}}}}
	c := &testLoadCollector{chaosPhases: []testLoadPhaseSamples{{}}}
	run := strings.Repeat("a", 32)
	for i := 0; i < chaos.MaxRequestCorrelationSlots+5; i++ {
		id := c.correlationID(cfg, "read", run, 0)
		if (id != "") != (i < chaos.MaxRequestCorrelationSlots) {
			t.Fatal("capacity not enforced")
		}
	}
	if c.chaosPhases[0].correlationOverflow != 5 {
		t.Fatal("overflow lost")
	}
	if c.correlationID(cfg, "healthy", run, 0) != "" {
		t.Fatal("healthy route consumed correlation IDs")
	}
}

func TestPerRequestAttemptsExecuteAgainstIndependentProxyCounters(t *testing.T) {
	for _, mode := range []string{"bounded", "masked outlier", "missing propagation", "missing receipt", "late counters", "legacy gateway", "phase overlap"} {
		t.Run(mode, func(t *testing.T) {
			body := strings.Replace(attemptAssertionManifest(false), "max_attempts_per_request: 2", "per_request: true, max_attempts_per_request: 2", 1)
			manifest, err := readAssertionProfileManifest(t, body)
			if err != nil {
				t.Fatal(err)
			}
			step := manifest.Scenarios["reusable-test"].Steps[1]
			var mu sync.Mutex
			active := false
			hadFault := false
			rule := chaos.Rule{}
			counts := map[string]int64{}
			incoming := 0
			run := strings.Repeat("a", 32)
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodPut:
					var plan api.InjectScenarioTestChaosRequest
					if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
						t.Error(err)
					}
					rule = chaos.Rule(plan.Rules[0])
					active = true
					hadFault = true
					_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "strict", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
				case http.MethodDelete:
					active = false
					w.WriteHeader(204)
				case http.MethodGet:
					matches := []api.ScenarioTestChaosMatch{}
					for id, count := range counts {
						matches = append(matches, api.ScenarioTestChaosMatch{RuleID: id, Count: count})
					}
					_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "strict", Matches: matches})
				}
			}))
			defer control.Close()
			dependency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if !active {
					w.WriteHeader(200)
					return
				}
				counts[chaos.RuleID(rule)]++
				if mode != "legacy gateway" {
					counts[chaos.CorrelationSupportID(rule)]++
					slot, ok := chaos.RequestCorrelationSlot(run, rule, r.Header.Get(chaos.RequestCorrelationHeader))
					if !ok {
						slot = 0
					}
					if mode != "late counters" {
						counts[chaos.CorrelationCounterID(rule, slot)]++
					}
				}
				w.WriteHeader(503)
			}))
			defer dependency.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/payment" {
					w.WriteHeader(200)
					return
				}
				mu.Lock()
				isFault := active
				beforeFault := !hadFault
				count := 1
				if isFault {
					incoming++
					if mode == "masked outlier" && incoming == 1 {
						count = 11
					}
				}
				mu.Unlock()
				if mode == "phase overlap" && beforeFault {
					time.Sleep(400 * time.Millisecond)
				}
				id := r.Header.Get(chaos.RequestCorrelationHeader)
				attempts := 0
				status := 200
				for i := 0; i < count; i++ {
					req, _ := http.NewRequest(http.MethodGet, dependency.URL, nil)
					if mode != "missing propagation" {
						req.Header.Set(chaos.RequestCorrelationHeader, id)
					}
					result, err := http.DefaultClient.Do(req)
					attempts++
					if err != nil {
						status = 503
						continue
					}
					status = result.StatusCode
					_ = result.Body.Close()
					if status == 200 {
						break
					}
				}
				if id != "" && mode != "missing receipt" {
					w.Header().Set(testRequestAttemptsHeader, fmt.Sprint(attempts))
				}
				w.WriteHeader(status)
			}))
			defer app.Close()
			report, _, runErr := runTestScenarioStepLoad(context.Background(), NewClient(control.URL, "token"), app.URL, run, nil, step, nil, nil)
			if report == nil {
				t.Fatal(runErr)
			}
			e := report.ChaosPhases[1].RetryAmplification
			pass := mode == "bounded" || mode == "phase overlap"
			if (runErr == nil) != pass || e == nil || e.PerRequest == nil || e.PerRequest.Passed != pass {
				t.Fatalf("%s: %v, %+v", mode, runErr, e)
			}
			if mode == "masked outlier" && (e.AttemptsPerRequest > 2 || e.PerRequest.ViolationCount != 1 || e.PerRequest.MaxAttempts != 11) {
				t.Fatalf("average did not conceal strict violation: %+v", e)
			}
			var terminal bytes.Buffer
			printTestLoadSummary(&terminal, report)
			if !strings.Contains(terminal.String(), "per-request attempts:") {
				t.Fatal("terminal lost strict evidence")
			}
			receipt := testRunReceipt{Scenario: "strict", Status: report.Status, Load: report}
			if runErr != nil {
				receipt.Error = runErr.Error()
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "report.xml")
			if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			var suite testJUnitSuite
			if err := xml.Unmarshal(data, &suite); err != nil {
				t.Fatal(err)
			}
			var decoded testRunReceipt
			if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Load.ChaosPhases[1].RetryAmplification.PerRequest == nil || (suite.Failures == 0) != pass {
				t.Fatal("JUnit/JSON lost strict verdict")
			}
			path = filepath.Join(dir, "report.html")
			if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(path)
			if !strings.Contains(html.UnescapeString(string(data)), "Per-request attempts:") {
				t.Fatal("HTML omitted strict report")
			}
		})
	}
}

func TestHTTPDependencyRestartPerRequestAttemptsExampleManifest(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-http-write/gregale-per-request-attempts-test.yaml", "per-request-attempts", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("strict example: %v", err)
	}
	p := prepared[0]
	if _, err := collectTestValidationForEngine(map[string]testScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
}

func TestPerRequestAttemptsCancellationAndSetupFailureReleaseDispatch(t *testing.T) {
	for _, mode := range []string{"already canceled", "setup failure"} {
		t.Run(mode, func(t *testing.T) {
			body := strings.Replace(attemptAssertionManifest(false), "max_attempts_per_request: 2", "per_request: true, max_attempts_per_request: 2", 1)
			manifest, err := readAssertionProfileManifest(t, body)
			if err != nil {
				t.Fatal(err)
			}
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "setup rejected", 503) }))
			defer control.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
			defer app.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "already canceled" {
				cancel()
			}
			started := time.Now()
			_, _, runErr := runTestScenarioStepLoad(ctx, NewClient(control.URL, "token"), app.URL, strings.Repeat("a", 32), nil, manifest.Scenarios["reusable-test"].Steps[1], nil, nil)
			if runErr == nil || time.Since(started) > 2500*time.Millisecond {
				t.Fatalf("dispatch did not terminate promptly: %v", runErr)
			}
		})
	}
}
