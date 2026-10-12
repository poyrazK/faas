package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestConnectionAttemptsValidation(t *testing.T) {
	phase := testLoadChaosPhaseSpec{Name: "fault", Rules: []testChaosRule{{From: "app", To: "dependency", Kind: "tcp_connect_refused", Port: 5432, Percent: 100}}, ConnectionAttempts: &testRetryAmplificationSpec{Request: "incoming", MinRequests: 10, MaxAttemptsPerRequest: 3}}
	if err := validateTestConnectionAttempts(phase); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"latency", "dns_timeout"} {
		p := phase
		p.Rules = []testChaosRule{{From: "app", To: "dependency", Kind: kind, Percent: 100}}
		if validateTestConnectionAttempts(p) == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
	phase.Rules[0].Percent = 50
	if validateTestConnectionAttempts(phase) == nil {
		t.Fatal("accepted sampled evidence")
	}
	phase.Rules[0].Percent = 100
	phase.ConnectionAttempts.MaxAttemptsPerRequest = math.NaN()
	if validateTestConnectionAttempts(phase) == nil {
		t.Fatal("accepted NaN")
	}
}

func TestConnectionAttemptsBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		attempts    int64
		requests    int
		missing     bool
		scheduleErr error
		pass        bool
	}{
		{name: "boundary", attempts: 30, requests: 10, pass: true},
		{name: "retry storm", attempts: 31, requests: 10},
		{name: "insufficient samples", attempts: 3, requests: 1},
		{name: "missing evidence", requests: 10, missing: true},
		{name: "capture failure", attempts: 20, requests: 10, scheduleErr: errors.New("capture failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{Name: "fault", Rules: []testChaosRule{{From: "app", To: "dependency", Port: 5432}}, ConnectionAttempts: &testRetryAmplificationSpec{Request: "incoming", MinRequests: 10, MaxAttemptsPerRequest: 3}}}}
			phase := testLoadChaosPhaseEvidence{Steps: []testLoadStepEvidence{{Name: "incoming", testLoadMetrics: testLoadMetrics{Requests: tc.requests}}, {Name: "health", testLoadMetrics: testLoadMetrics{Requests: 1000}}}}
			if !tc.missing {
				phase.Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Matches: 999, TCPAttempts: &tc.attempts}}}
			}
			report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{phase}}
			err := applyTestConnectionAttempts(report, cfg, tc.scheduleErr)
			evidence := report.ChaosPhases[0].ConnectionAttempts
			if (err == nil) != tc.pass || evidence.Passed != tc.pass || evidence.Requests != tc.requests {
				t.Fatalf("err %v, evidence %+v", err, evidence)
			}
			if !tc.pass && report.Status != "failed" {
				t.Fatal("did not fail load")
			}
		})
	}
}

func TestConnectionAttemptsConfigurationAndReports(t *testing.T) {
	request := loadTestStep()
	spec := testLoadSpec{Duration: "3s", ChaosSchedule: []testLoadChaosPhaseSpec{
		{Name: "baseline", Duration: "1s"},
		{Name: "fault", Duration: "1s", Rules: []testChaosRule{{From: "app", To: "inventory", Kind: "tcp_connect_refused", Port: 5432, Percent: 100}}, ConnectionAttempts: &testRetryAmplificationSpec{Request: request.Name, MinRequests: 10, MaxAttemptsPerRequest: 3}},
		{Name: "recovery", Duration: "1s"},
	}}
	scenario := testScenario{Requests: []testHTTPRequest{request}, Load: &spec}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	spec.ChaosSchedule[1].ConnectionAttempts.Request = "missing"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
		t.Fatal("accepted missing request selector")
	}
	spec.ChaosSchedule[1].ConnectionAttempts.Request = request.Name
	report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "baseline"}, {Name: "fault", Chaos: &testChaosEvidence{Matches: []testChaosRuleEvidence{{Matches: 999, TCPAttempts: func() *int64 { n := int64(31); return &n }()}}}, Steps: []testLoadStepEvidence{{Name: request.Name, testLoadMetrics: testLoadMetrics{Requests: 10}}}}, {Name: "recovery"}}}
	err = applyTestConnectionAttempts(report, cfg, nil)
	if err == nil {
		t.Fatal("retry storm passed")
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "31 attempts / 10") {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "retry", Status: "failed", Error: err.Error(), Load: report}
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
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.Load.ChaosPhases[1].ConnectionAttempts.Attempts != 31 {
		t.Fatalf("JSON evidence missing: %v", err)
	}
	path = filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "31 attempts / 10") {
		t.Fatal("HTML evidence missing")
	}
}

func TestConnectionAttemptsCaptureSeparateEvidence(t *testing.T) {
	rule := chaos.Rule{From: "app", To: "cache", Kind: chaos.KindTCPTimeout, Percent: 100, Port: 6379}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "generation", Matches: []api.ScenarioTestChaosMatch{{RuleID: chaos.RuleID(rule), Count: 2}, {RuleID: chaos.TCPAttemptID(rule), Count: 7}}})
	}))
	defer server.Close()
	evidence := &testChaosEvidence{Generation: "generation", Matches: []testChaosRuleEvidence{{Rule: rule}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := captureTestChaosMatches(ctx, NewClient(server.URL, "token"), "run", evidence); err != nil {
		t.Fatal(err)
	}
	observed := evidence.Matches[0]
	if observed.Matches != 2 || observed.TCPAttempts == nil || *observed.TCPAttempts != 7 {
		t.Fatalf("counts merged or missing: %+v", observed)
	}
}

func TestConnectionAttemptsRejectsLegacyGatewayEvidence(t *testing.T) {
	cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{Name: "fault", Rules: []testChaosRule{{From: "app", To: "cache", Port: 6379}}, ConnectionAttempts: &testRetryAmplificationSpec{Request: "incoming", MinRequests: 1, MaxAttemptsPerRequest: 3}}}}
	report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{{Chaos: &testChaosEvidence{Matches: []testChaosRuleEvidence{{Matches: 1}}}, Steps: []testLoadStepEvidence{{Name: "incoming", testLoadMetrics: testLoadMetrics{Requests: 10}}}}}}
	if err := applyTestConnectionAttempts(report, cfg, nil); err == nil || !strings.Contains(err.Error(), "connection-attempt support") {
		t.Fatalf("legacy evidence accepted: %v", err)
	}
}
