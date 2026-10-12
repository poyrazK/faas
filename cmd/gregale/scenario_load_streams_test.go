package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func streamTestScenario() testScenario {
	slow, health := loadTestStep(), loadTestStep()
	slow.Name = "checkout"
	slow.Path = "/checkout"
	health.Name = "health"
	health.Path = "/health"
	return testScenario{Requests: []testHTTPRequest{slow, health}, Load: &testLoadSpec{Duration: "1s", Streams: []testLoadStreamSpec{{Name: "checkout-load", Requests: []string{"checkout"}, Rate: 5, VUs: 1}, {Name: "health-probes", Requests: []string{"health"}, Rate: 5, VUs: 1}}}}
}

func TestLoadStreamsValidation(t *testing.T) {
	scenario := streamTestScenario()
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil || cfg.maxVUs() != 2 {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	mutations := []func(*testScenario){
		func(s *testScenario) { s.Load.VUs = 1 },
		func(s *testScenario) { s.Load.Streams[0].VUs = 50 },
		func(s *testScenario) { s.Load.Streams[0].Rate = 1000 },
		func(s *testScenario) { s.Load.Streams[1].Name = s.Load.Streams[0].Name },
		func(s *testScenario) { s.Load.Streams[1].Requests = []string{"checkout"} },
		func(s *testScenario) { s.Load.Streams[1].Requests = []string{"unknown"} },
		func(s *testScenario) { s.Load.Streams[1].Requests = nil },
		func(s *testScenario) {
			s.Load.Duration = "5m"
			s.Load.Streams[0].Rate = 500
			s.Load.Streams[1].Rate = 500
		},
		func(s *testScenario) {
			s.Requests[0].Capture = map[string]string{"id": "/id"}
			s.Requests[1].Path = "/${steps.checkout.id}"
		},
	}
	for i, mutate := range mutations {
		s := streamTestScenario()
		mutate(&s)
		if _, err := resolveTestLoadConfig(s, testLoadOverrides{}); err == nil {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{VUsSet: true, VUs: 2}); err == nil {
		t.Fatal("override accepted")
	}
	// Unused streams must not change historical recovery fingerprints.
	encoded, _ := json.Marshal(testLoadSpec{})
	if strings.Contains(string(encoded), "Streams") {
		t.Fatal("empty streams changes serialization")
	}
}

func TestLoadStreamsHealthContinuesDuringSlowFailedCheckout(t *testing.T) {
	var health atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checkout" {
			select {
			case <-time.After(600 * time.Millisecond):
				w.WriteHeader(503)
			case <-r.Context().Done():
			}
			return
		}
		health.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	scenario := streamTestScenario()
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ErrorRate = 1
	start := time.Now()
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(300*time.Millisecond))
	clock.transition(2, start.Add(700*time.Millisecond))
	clock.finish(start.Add(time.Second))
	cfg.ChaosPhaseClock = clock
	cfg.ChaosSchedule = []testLoadChaosPhase{{Name: "baseline"}, {Name: "fault", StepThresholds: map[string]testLoadStepThresholdConfig{"health": {P95: 200 * time.Millisecond, MinSamples: 1}}}, {Name: "recovery"}}
	report, err := runTestLoad(context.Background(), server.URL, "streams", nil, scenario.Requests, nil, cfg)
	if err == nil || !strings.Contains(err.Error(), "checkout-load") || report.StopReason != "dropped_arrivals" {
		t.Fatalf("dropped slow stream didn't fail: %v %+v", err, report)
	}
	if health.Load() != 5 || report.Steps[1].Requests != 5 || report.Streams[1].Arrival.Dropped != 0 {
		t.Fatalf("health probes starved: %+v", report)
	}
	if report.ChaosPhases[1].Steps[1].Requests < 1 || !report.ChaosPhases[1].Steps[1].Thresholds[0].Passed {
		t.Fatal("health phase evidence missing")
	}
	if report.IterationsStarted != report.IterationsCompleted || report.Requests != report.IterationsStarted || report.PeakVUs > 2 {
		t.Fatalf("invalid accounting: %+v", report)
	}
	var out bytes.Buffer
	printTestLoadSummary(&out, report)
	if !strings.Contains(out.String(), "stream health-probes") {
		t.Fatal("missing stream summary")
	}
	encoded, _ := json.Marshal(report)
	var decoded testLoadEvidence
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded.Streams) != 2 {
		t.Fatal("stream evidence lost")
	}
}

func TestLoadStreamsCancellationJoinsRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	scenario := streamTestScenario()
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	report, err := runTestLoad(ctx, server.URL, "cancel", nil, scenario.Requests, nil, cfg)
	if err == nil || report.StopReason != "scenario_timeout" || report.IterationsInterrupted != 2 {
		t.Fatalf("cancellation accounting: %+v %v", report, err)
	}
}

func TestLoadStreamsJourneyCapturesAndReports(t *testing.T) {
	scenario := streamTestScenario()
	scenario.Requests[0].Path = "/create/${run.id}"
	scenario.Requests[0].Capture = map[string]string{"id": "/id"}
	follow := loadTestStep()
	follow.Name = "follow"
	follow.Path = "/follow/${steps.checkout.id}/${run.id}"
	scenario.Requests = []testHTTPRequest{scenario.Requests[0], follow, scenario.Requests[1]}
	scenario.Load.Streams[0].Requests = []string{"checkout", "follow"}
	scenario.Load.Streams[0].Rate = 2
	scenario.Load.Streams[1].Rate = 2
	var mismatches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		switch parts[0] {
		case "create":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": parts[1]})
		case "follow":
			if len(parts) != 3 || parts[1] != parts[2] {
				mismatches.Add(1)
				w.WriteHeader(400)
			}
		default:
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runTestLoad(context.Background(), server.URL, "capture", nil, scenario.Requests, nil, cfg)
	if err != nil || mismatches.Load() != 0 || report.Requests != 6 || report.IterationsCompleted != 4 {
		t.Fatalf("journey captures/accounting: %+v %v", report, err)
	}
	if !testLoadScheduleMatches(report, cfg) {
		t.Fatal("stream schedule does not match its report")
	}
	if err := validateTestBenchmarkLoad(report, scenario); err != nil {
		t.Fatalf("valid stream benchmark rejected: %v", err)
	}
	changed := *cfg
	changed.Streams = append([]testLoadStreamSpec(nil), cfg.Streams...)
	changed.Streams[0].Rate++
	if testLoadScheduleMatches(report, &changed) {
		t.Fatal("changed stream rate accepted as same schedule")
	}
	plan := testRunPlan{Prepared: &testPreparedScenario{Scenario: scenario, Load: cfg}}
	before, err := testPlanWorkload(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Prepared.Load = &changed
	after, err := testPlanWorkload(plan)
	if err != nil || before.SHA256 == after.SHA256 {
		t.Fatal("stream rate missing from workload identity")
	}
	report.Streams[0].Arrival.Dropped = 1
	if err := validateTestBenchmarkLoad(report, scenario); err == nil {
		t.Fatal("dropped stream baseline accepted")
	}
	report.Streams[0].Arrival.Dropped = 0
	receipt := testRunReceipt{Scenario: "streams", Status: "passed", Load: report, Steps: []testScenarioStepEvidence{{Name: "load", Status: "passed", Load: report}}}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Count(string(body), "Stream health-probes:") != 2 {
		t.Fatalf("root/staged HTML streams missing: %v", err)
	}
	path = filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 0 {
		t.Fatalf("JUnit: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || len(decoded.Load.Streams) != 2 {
		t.Fatal(fmt.Sprintf("JUnit stream evidence lost: %v", err))
	}
}
