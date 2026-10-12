package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetryAmplificationValidation(t *testing.T) {
	phase := testLoadChaosPhaseSpec{Name: "fault", Rules: []testChaosRule{{From: "app", To: "dependency", Kind: "http_status", Percent: 100}}, RetryAmplification: &testRetryAmplificationSpec{Request: "incoming", MinRequests: 10, MaxAttemptsPerRequest: 3}}
	if err := validateTestRetryAmplification(phase); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"tcp_reset", "dns_timeout"} {
		p := phase
		p.Rules = []testChaosRule{{From: "app", To: "dependency", Kind: kind, Percent: 100}}
		if validateTestRetryAmplification(p) == nil {
			t.Fatalf("accepted %s", kind)
		}
	}
	phase.Rules[0].Percent = 50
	if validateTestRetryAmplification(phase) == nil {
		t.Fatal("accepted sampled evidence")
	}
	phase.Rules[0].Percent = 100
	phase.RetryAmplification.MaxAttemptsPerRequest = math.NaN()
	if validateTestRetryAmplification(phase) == nil {
		t.Fatal("accepted NaN")
	}
}

func TestRetryAmplificationBudget(t *testing.T) {
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
			cfg := &testLoadConfig{ChaosSchedule: []testLoadChaosPhase{{Name: "fault", Rules: []testChaosRule{{From: "app", To: "dependency"}}, RetryAmplification: &testRetryAmplificationSpec{Request: "incoming", MinRequests: 10, MaxAttemptsPerRequest: 3}}}}
			phase := testLoadChaosPhaseEvidence{Steps: []testLoadStepEvidence{{Name: "incoming", testLoadMetrics: testLoadMetrics{Requests: tc.requests}}, {Name: "health", testLoadMetrics: testLoadMetrics{Requests: 1000}}}}
			if !tc.missing {
				phase.Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Matches: tc.attempts}}}
			}
			report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{phase}}
			err := applyTestRetryAmplification(report, cfg, tc.scheduleErr)
			evidence := report.ChaosPhases[0].RetryAmplification
			if (err == nil) != tc.pass || evidence.Passed != tc.pass || evidence.Requests != tc.requests {
				t.Fatalf("err %v, evidence %+v", err, evidence)
			}
			if !tc.pass && report.Status != "failed" {
				t.Fatal("did not fail load")
			}
		})
	}
}

func TestRetryAmplificationConfigurationAndReports(t *testing.T) {
	request := loadTestStep()
	spec := testLoadSpec{Duration: "3s", ChaosSchedule: []testLoadChaosPhaseSpec{
		{Name: "baseline", Duration: "1s"},
		{Name: "fault", Duration: "1s", Rules: []testChaosRule{{From: "app", To: "inventory", Kind: "http_status", Percent: 100, StatusCode: 503}}, RetryAmplification: &testRetryAmplificationSpec{Request: request.Name, MinRequests: 10, MaxAttemptsPerRequest: 3}},
		{Name: "recovery", Duration: "1s"},
	}}
	scenario := testScenario{Requests: []testHTTPRequest{request}, Load: &spec}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	spec.ChaosSchedule[1].RetryAmplification.Request = "missing"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
		t.Fatal("accepted missing request selector")
	}
	spec.ChaosSchedule[1].RetryAmplification.Request = request.Name
	report := &testLoadEvidence{Status: "passed", ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "baseline"}, {Name: "fault", Chaos: &testChaosEvidence{Matches: []testChaosRuleEvidence{{Matches: 31}}}, Steps: []testLoadStepEvidence{{Name: request.Name, testLoadMetrics: testLoadMetrics{Requests: 10}}}}, {Name: "recovery"}}}
	err = applyTestRetryAmplification(report, cfg, nil)
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
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.Load.ChaosPhases[1].RetryAmplification.Attempts != 31 {
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
