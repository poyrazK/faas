package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
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

const assertionProfileManifest = `version: 1
assertion_profiles:
  healthy:
    healthy_routes:
      healthy-read: {p95: 50ms, error_rate: 0, min_samples: 1}
  recovered:
    recovery_deadlines:
      payment-read:
        within: 500ms
        sustain: 200ms
        window: 200ms
        thresholds: {p95: 500ms, error_rate: 0, min_samples: 1}
scenarios:
  reusable-test:
    project: reusable-test
    source: .
    services:
      cache: {source: .}
    steps:
      - name: baseline
        requests:
          - {name: baseline, method: GET, path: /healthy, expect: {status: 200}}
      - name: exercise
        requests:
          - {name: healthy-read, method: GET, path: /healthy, expect: {status: 200}}
          - {name: payment-read, method: GET, path: /payment, expect: {status: 200}}
        load:
          vus: 1
          duration: 3s
          pacing: 5ms
          thresholds: {error_rate: 1}
          chaos_schedule:
            - {name: baseline, duration: 1s, assertions: [healthy]}
            - name: fault
              duration: 1s
              assertions: [healthy]
              rules: [{to: cache, kind: latency, latency: 100ms, percent: 100}]
            - {name: recovery, duration: 1s, assertions: [healthy, recovered]}
      - name: restored
        requests:
          - {name: restored, method: GET, path: /payment, expect: {status: 200}}
`

func readAssertionProfileManifest(t *testing.T, body string) (testManifest, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gregale-test.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readTestManifestDocument(path, func(string, testScenario) []string { return nil })
	return manifest, err
}

func TestAssertionProfilesRejectAmbiguousMissingAndInvalidBudgets(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":                strings.Replace(assertionProfileManifest, "assertions: [healthy]", "assertions: [missing]", 1),
		"duplicate":              strings.Replace(assertionProfileManifest, "assertions: [healthy]", "assertions: [healthy, healthy]", 1),
		"inline conflict":        strings.Replace(assertionProfileManifest, "assertions: [healthy]}", "assertions: [healthy], step_thresholds: {healthy-read: {p95: 1s, min_samples: 1}}}", 1),
		"unknown route":          strings.Replace(assertionProfileManifest, "healthy-read: {p95:", "undeclared: {p95:", 1),
		"missing samples":        strings.Replace(assertionProfileManifest, "p95: 50ms, error_rate: 0, min_samples: 1", "p95: 50ms, error_rate: 0", 1),
		"no success budget":      strings.Replace(assertionProfileManifest, "p95: 50ms, error_rate: 0", "p95: 50ms", 1),
		"deadline exceeds phase": strings.Replace(assertionProfileManifest, "within: 500ms", "within: 2s", 1),
		"recovery during fault":  strings.Replace(assertionProfileManifest, "assertions: [healthy]\n              rules:", "assertions: [recovered]\n              rules:", 1),
		"unknown field":          strings.Replace(assertionProfileManifest, "healthy_routes:", "healthy_route:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readAssertionProfileManifest(t, body); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}

func TestAssertionProfileReuseDoesNotShareBudgetsAndReplayBindsEffectiveLimits(t *testing.T) {
	manifest, err := readAssertionProfileManifest(t, assertionProfileManifest)
	if err != nil {
		t.Fatal(err)
	}
	scenario := manifest.Scenarios["reusable-test"]
	phases := scenario.Steps[1].Load.ChaosSchedule
	*phases[0].StepThresholds["healthy-read"].ErrorRate = 0.5
	if *phases[1].StepThresholds["healthy-read"].ErrorRate != 0 || *manifest.AssertionProfiles["healthy"].HealthyRoutes["healthy-read"].ErrorRate != 0 {
		t.Fatal("profile reused mutable budget")
	}
	other, err := expandTestAssertionProfiles(testScenario{Steps: []testScenarioStep{{Name: "other", Load: &testLoadSpec{ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "recovery", Assertions: []string{"recovered"}}}}}}}, manifest.AssertionProfiles)
	if err != nil {
		t.Fatal(err)
	}
	phases[2].StepRecovery["payment-read"].Within = "900ms"
	if other.Steps[0].Load.ChaosSchedule[0].StepRecovery["payment-read"].Within != "500ms" || manifest.AssertionProfiles["recovered"].RecoveryDeadlines["payment-read"].Within != "500ms" {
		t.Fatal("recovery profile shared state across scenarios")
	}
	before := newTestReplayManifest("reusable-test", "warm", "", scenario)
	budget := phases[1].StepThresholds["healthy-read"]
	budget.P95 = "100ms"
	phases[1].StepThresholds["healthy-read"] = budget
	after := newTestReplayManifest("reusable-test", "warm", "", scenario)
	if before == nil || after == nil || before.ConfigurationSHA256 == after.ConfigurationSHA256 {
		t.Fatal("replay did not bind expanded assertion limits")
	}
}

func TestAssertionProfilesExecuteAndReportMeasuredFailures(t *testing.T) {
	for _, kind := range []string{"pass", "slow healthy", "never recovers"} {
		t.Run(kind, func(t *testing.T) {
			manifest, err := readAssertionProfileManifest(t, assertionProfileManifest)
			if err != nil {
				t.Fatal(err)
			}
			var fault, hadFault atomic.Bool
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPut:
					fault.Store(true)
					hadFault.Store(true)
					_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "profiles", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
				case http.MethodDelete:
					fault.Store(false)
					w.WriteHeader(http.StatusNoContent)
				case http.MethodGet:
					_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "profiles"})
				}
			}))
			defer control.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthy" && kind == "slow healthy" {
					time.Sleep(80 * time.Millisecond)
				}
				if r.URL.Path == "/payment" && (fault.Load() || kind == "never recovers" && hadFault.Load()) {
					w.WriteHeader(503)
					return
				}
				w.WriteHeader(200)
			}))
			defer app.Close()
			step := manifest.Scenarios["reusable-test"].Steps[1]
			report, _, runErr := runTestScenarioStepLoad(context.Background(), NewClient(control.URL, "token"), app.URL, "0123456789abcdef0123456789abcdef", nil, step, nil, nil)
			if report == nil {
				t.Fatal(runErr)
			}
			if (runErr == nil) != (kind == "pass") {
				t.Fatalf("%s: %v, report %+v", kind, runErr, report)
			}
			if len(report.ChaosPhases[2].Assertions) != 2 || report.ChaosPhases[2].StepRecovery["payment-read"] == nil {
				t.Fatal("expanded assertions missing from evidence")
			}
			if kind == "slow healthy" && !strings.Contains(runErr.Error(), "healthy-read") {
				t.Fatal(runErr)
			}
			if kind == "never recovers" && !strings.Contains(runErr.Error(), "recovery failed") {
				t.Fatal(runErr)
			}
			receipt := testRunReceipt{Scenario: "reusable-test", Status: report.Status, Steps: []testScenarioStepEvidence{{Name: step.Name, Status: report.Status, Load: report}}}
			if runErr != nil {
				receipt.Error = runErr.Error()
			}
			path := filepath.Join(t.TempDir(), "results.xml")
			if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var suite testJUnitSuite
			if err := xml.Unmarshal(body, &suite); err != nil {
				t.Fatal(err)
			}
			var decoded testRunReceipt
			if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Steps[0].Load.ChaosPhases[2].Assertions) != 2 {
				t.Fatal("JUnit embedded JSON lost profiles")
			}
			if (suite.Failures == 0) != (kind == "pass") {
				t.Fatal("JUnit lost assertion failure")
			}
			path = filepath.Join(t.TempDir(), "results.html")
			if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			body, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rendered := html.UnescapeString(string(body))
			for _, text := range []string{"Assertion profile: healthy", "healthy-read", "payment-read", "within 500ms"} {
				if !strings.Contains(rendered, text) {
					t.Fatalf("HTML omitted %q", text)
				}
			}
		})
	}
}

func TestHTTPDependencyRestartAssertionProfilesExampleManifest(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-http-write/gregale-assertions-test.yaml", "http-assertions", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("assertions example: %v", err)
	}
	p := prepared[0]
	if _, err := collectTestValidationForEngine(map[string]testScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if len(p.Scenario.Steps[1].Load.ChaosSchedule[2].Assertions) != 2 {
		t.Fatal("example profiles not expanded")
	}
}

func TestAssertionProfilesPreserveUnreferencedConfigurationFingerprint(t *testing.T) {
	for _, scenario := range []testScenario{
		{},
		{Steps: []testScenarioStep{}, Load: &testLoadSpec{ChaosSchedule: []testLoadChaosPhaseSpec{}}},
		{Steps: []testScenarioStep{{Name: "load", Load: &testLoadSpec{ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "phase", StepThresholds: map[string]testLoadStepThresholdSpec{}}}}}}},
	} {
		before, err := json.Marshal(scenario)
		if err != nil {
			t.Fatal(err)
		}
		expanded, err := expandTestAssertionProfiles(scenario, nil)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(expanded)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("unreferenced scenario fingerprint changed: %s -> %s", before, after)
		}
	}
}
