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
	"testing"
	"time"
)

func endpointRecoverySpec() *testLoadRecoverySpec {
	zero := 0.0
	return &testLoadRecoverySpec{Within: "2s", Sustain: "1s", Window: "1s", Thresholds: testLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 5}}
}
func endpointRecoveryScenario() testScenario {
	checkout, health := loadTestStep(), loadTestStep()
	checkout.Name = "checkout"
	health.Name = "health"
	return testScenario{Requests: []testHTTPRequest{checkout, health}, Load: &testLoadSpec{Duration: "5s", ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "1s"}, {Name: "fault", Duration: "1s", Rules: []testChaosRule{{To: "cache", Kind: "http_status", Percent: 100, StatusCode: 503}}}, {Name: "recovery", Duration: "3s", StepRecovery: map[string]*testLoadRecoverySpec{"checkout": endpointRecoverySpec()}}}}}
}
func TestEndpointRecoveryValidation(t *testing.T) {
	scenario := endpointRecoveryScenario()
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil || cfg.ChaosSchedule[2].StepRecovery["checkout"].Within != 2*time.Second {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	mutations := []func(*testScenario){
		func(s *testScenario) { s.Load.ChaosSchedule[2].StepRecovery["checkout"] = nil },
		func(s *testScenario) {
			s.Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"unknown": endpointRecoverySpec()}
		},
		func(s *testScenario) { s.Load.ChaosSchedule[2].StepRecovery["checkout"].Thresholds.MinSamples = 0 },
		func(s *testScenario) { s.Load.ChaosSchedule[2].StepRecovery["checkout"].Sustain = "2s" },
		func(s *testScenario) { s.Load.ChaosSchedule[2].StepRecovery["checkout"].Window = "3s" },
		func(s *testScenario) { s.Load.ChaosSchedule[0].StepRecovery = s.Load.ChaosSchedule[2].StepRecovery },
		func(s *testScenario) { s.Load.ChaosSchedule[1].StepRecovery = s.Load.ChaosSchedule[2].StepRecovery },
	}
	for i, mutate := range mutations {
		s := endpointRecoveryScenario()
		mutate(&s)
		if _, err := resolveTestLoadConfig(s, testLoadOverrides{}); err == nil {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
	encoded, _ := json.Marshal(testLoadChaosPhaseSpec{})
	if strings.Contains(string(encoded), "StepRecovery") {
		t.Fatal("unused field changes old recovery fingerprints")
	}
}
func endpointRecoveryReport(mode string) *testLoadEvidence {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(3*time.Second))
	clock.transition(2, start.Add(4*time.Second))
	clock.finish(start.Add(8 * time.Second))
	aggregateLimit := 0.1
	aggregate := resolveTestLoadRecovery(endpointRecoverySpec())
	aggregate.Thresholds.ErrorRate = &aggregateLimit
	budget := resolveTestLoadRecovery(endpointRecoverySpec())
	cfg := &testLoadConfig{VUs: 2, Duration: 8 * time.Second, ErrorRate: 1, ChaosPhaseClock: clock, ChaosSchedule: []testLoadChaosPhase{{Name: "baseline", Duration: 3 * time.Second}, {Name: "fault", Duration: time.Second}, {Name: "recovery", Duration: 4 * time.Second, Recovery: aggregate, StepRecovery: map[string]*testLoadRecoveryConfig{"checkout": budget}}}}
	if mode == "stability" {
		budget.Stability = time.Second
	}
	if mode == "relative" {
		increase := 10.0
		budget.Relative = &testLoadRecoveryRelativeSpec{P95IncreasePercent: &increase, BaselineMinSamples: 20}
	}
	makeSteps := func() []testLoadStepSamples {
		return []testLoadStepSamples{{evidence: testLoadStepEvidence{Name: "checkout", testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}}, {evidence: testLoadStepEvidence{Name: "health", testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}}}
	}
	collector := &testLoadCollector{phaseClock: clock, steps: makeSteps(), chaosPhases: []testLoadPhaseSamples{{collectRecovery: true, steps: makeSteps()}, {steps: makeSteps()}, {collectRecovery: true, steps: makeSteps()}}}
	for i := 0; i < 30; i++ {
		collector.recordAt(0, testHTTPRequestEvidence{Passed: true, Status: 200}, 10*time.Millisecond, start.Add(time.Duration(i)*100*time.Millisecond))
		collector.recordAt(1, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	for i := 0; i < 400; i++ {
		at := start.Add(4*time.Second + time.Duration(i)*10*time.Millisecond)
		collector.recordAt(1, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, at)
		if i%10 != 0 || mode == "empty" || (mode == "weak" && i%100 != 0) {
			continue
		}
		duration := 10 * time.Millisecond
		passed := true
		switch mode {
		case "failed":
			passed = false
		case "relative":
			duration = 20 * time.Millisecond
		case "pending":
			duration = 10 * time.Second
		case "stability":
			passed = i != 250
		case "unstable":
			passed = i != 150
		case "late":
			passed = i >= 200
		case "healthy":
			passed = i >= 5
		}
		collector.recordAt(0, testHTTPRequestEvidence{Passed: passed, Status: 200}, duration, at)
	}
	report := collector.report(cfg, 8*time.Second)
	report.Status = "passed"
	if !testLoadThresholdsPassed(report) {
		report.Status = "failed"
	}
	return report
}
func TestEndpointRecoveryCannotBeMaskedByHealthTraffic(t *testing.T) {
	for _, mode := range []string{"failed", "relative", "pending", "empty", "weak", "unstable", "late", "stability", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			report := endpointRecoveryReport(mode)
			phase := report.ChaosPhases[2]
			if !phase.Recovery.Passed {
				t.Fatalf("aggregate fixture failed: %+v", phase.Recovery)
			}
			r := phase.StepRecovery["checkout"]
			if r.Passed != (mode == "healthy") || testLoadThresholdsPassed(report) != (mode == "healthy") {
				t.Fatalf("endpoint recovery masked: %+v", r)
			}
			if mode == "healthy" && (r.RecoveryMS == nil || *r.RecoveryMS != 1100) {
				t.Fatalf("wrong endpoint recovery time: %+v", r)
			}
			if mode == "relative" && (r.Baseline == nil || r.Baseline.Requests != 30 || r.Baseline.Latency.P95MS != 10) {
				t.Fatalf("baseline mixed endpoint samples: %+v", r)
			}
			if mode != "healthy" && !strings.Contains(testLoadThresholdFailure(report).Error(), `HTTP step "checkout" recovery failed`) {
				t.Fatal("endpoint failure unnamed")
			}
		})
	}
}
func TestEndpointRecoveryReportsAndRepeatedSummaries(t *testing.T) {
	report := endpointRecoveryReport("healthy")
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), `HTTP step "checkout" recovery: passed=true, observed=1100.000ms`) {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "endpoint-recovery", Status: "passed", Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}, Load: report}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Count(string(body), "HTTP step checkout recovery: passed") != 2 {
		t.Fatalf("HTML root/staged endpoint evidence missing: %v", err)
	}
	failed := endpointRecoveryReport("failed")
	receipt.Status = "failed"
	receipt.Error = testLoadThresholdFailure(failed).Error()
	receipt.Load = failed
	receipt.Steps[0].Load = failed
	path = filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Message, `HTTP step "checkout"`) {
		t.Fatalf("JUnit failure missing: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.Load.ChaosPhases[2].StepRecovery["checkout"].Passed {
		t.Fatalf("JSON endpoint failure lost: %v", err)
	}
	history := []testRunReceipt{{Attempt: 1, Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}}, {Attempt: 2, Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}}}
	summaries := summarizeTestResilienceRecovery(history)
	if len(summaries) != 2 || summaries[0].Request != "" || summaries[1].Request != "checkout" || summaries[1].P95MS != 1100 {
		t.Fatalf("endpoint summary mixed: %+v", summaries)
	}
	scenario := endpointRecoveryScenario()
	scenario.Steps = []testScenarioStep{{Name: "load", Load: scenario.Load}}
	scenario.Steps[0].Load.ChaosSchedule[2].Recovery = endpointRecoverySpec()
	group := &testRunReceipt{Resilience: &testResilienceEvidence{Runs: 2, History: history}}
	if _, err := recoverySamples(group, scenario, 2); err != nil {
		t.Fatal(err)
	}
	history[0].Steps[0].Load = endpointRecoveryReport("failed")
	if _, err := recoverySamples(group, scenario, 2); err == nil || !strings.Contains(err.Error(), "checkout") {
		t.Fatalf("missing endpoint samples accepted: %v", err)
	}
}
func TestEndpointRecoveryFailsContinuousIndependentStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checkout" {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	scenario := streamTestScenario()
	scenario.Load.Duration = "3s"
	scenario.Load.Streams[0].VUs = 2
	scenario.Load.Streams[1].VUs = 2
	scenario.Load.Streams[1].Rate = 50
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ErrorRate = 1
	zero, aggregateLimit := 0.0, 0.2
	budget := &testLoadRecoveryConfig{Within: 500 * time.Millisecond, Sustain: 400 * time.Millisecond, Window: 300 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 1}}
	aggregate := *budget
	aggregate.Thresholds.ErrorRate = &aggregateLimit
	aggregate.Thresholds.MinSamples = 10
	clock := newTestLoadPhaseClock(3)
	cfg.ChaosPhaseClock = clock
	cfg.OnStart = func(start time.Time) {
		clock.start(start)
		clock.transition(1, start.Add(time.Second))
		clock.transition(2, start.Add(2*time.Second))
		clock.finish(start.Add(3 * time.Second))
	}
	cfg.ChaosSchedule = []testLoadChaosPhase{{Name: "baseline", Duration: time.Second}, {Name: "fault", Duration: time.Second}, {Name: "recovery", Duration: time.Second, Recovery: &aggregate, StepRecovery: map[string]*testLoadRecoveryConfig{"checkout": budget}}}
	report, err := runTestLoad(context.Background(), server.URL, "endpoint-streams", nil, scenario.Requests, nil, cfg)
	if err == nil || !strings.Contains(err.Error(), `HTTP step "checkout" recovery failed`) || !report.ChaosPhases[2].Recovery.Passed || report.ChaosPhases[2].StepRecovery["checkout"].Passed {
		t.Fatalf("stream endpoint failure masked: %v; aggregate=%+v, endpoint=%+v", err, report.ChaosPhases[2].Recovery, report.ChaosPhases[2].StepRecovery["checkout"])
	}
}

func TestEndpointRecoveryResilienceMatrixAndRegression(t *testing.T) {
	scenario := faultMatrixTestScenario()
	phase := &scenario.Steps[1].Load.ChaosSchedule[2]
	phase.StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": phase.Recovery}
	phase.Recovery = nil
	if err := validateTestResilience(scenario); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTestFaultMatrixCase(scenario, scenario.Resilience.FaultMatrix[0]); err != nil {
		t.Fatal(err)
	}
	plan := recoveryRegressionPlan()
	phase = &plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[2]
	phase.StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": phase.Recovery}
	makeGroup := func(endpoint float64) testRunReceipt {
		group := recoveryRegressionGroup(plan.Prepared.Scenario, 100)
		for i := range group.Resilience.History {
			value := endpoint
			group.Resilience.History[i].Steps[0].Load.ChaosPhases[0].StepRecovery = map[string]*testLoadRecoveryEvidence{"load-probe": {Passed: true, RecoveryMS: &value}}
		}
		return group
	}
	baseline := makeGroup(100)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := writeTestReport(path, []testRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	plans := []testRunPlan{plan}
	if err := prepareTestRecoveryBaselines(path, plans); err != nil {
		t.Fatal(err)
	}
	current := makeGroup(121)
	applyTestRecoveryBaseline(plans[0], &current)
	if current.Status != "failed" || len(current.RecoveryRegression.Checks) != 2 || current.RecoveryRegression.Checks[1].Request != "load-probe" || !current.RecoveryRegression.Checks[0].Passed || current.RecoveryRegression.Checks[1].Passed || !strings.Contains(current.Error, "recovery/load-probe") {
		t.Fatalf("aggregate hid endpoint regression: %+v", current.RecoveryRegression)
	}
	current = makeGroup(120)
	applyTestRecoveryBaseline(plans[0], &current)
	if current.Status != "passed" {
		t.Fatal(current.Error)
	}
}
