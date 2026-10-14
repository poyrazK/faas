package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func attemptAssertionManifest(tcp bool) string {
	field, rule := "dependency_attempts", "{from: reusable-test, to: cache, kind: http_status, status_code: 503, percent: 100, seed: 7}"
	service := "cache: {source: .}"
	if tcp {
		field, rule = "connection_attempts", "{from: reusable-test, to: cache, kind: tcp_reset, port: 6379, percent: 100, seed: 7}"
		service = "cache: {source: ., tcp_ports: [6379]}"
	}
	body := strings.Replace(assertionProfileManifest, "  healthy:\n", "  attempts:\n    "+field+": {request: payment-read, max_attempts_per_request: 2, min_requests: 3}\n  healthy:\n", 1)
	body = strings.Replace(body, "cache: {source: .}", service, 1)
	body = strings.Replace(body, "assertions: [healthy]\n              rules: [{to: cache, kind: latency, latency: 100ms, percent: 100}]", "assertions: [healthy, attempts]\n              rules: ["+rule+"]", 1)
	return body
}

func TestAssertionAttemptProfilesRejectConflictsAndIncompleteCoverage(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		body := attemptAssertionManifest(tcp)
		field, inline := "dependency_attempts", "retry_amplification"
		if tcp {
			field, inline = "connection_attempts", "connection_attempts"
		}
		if _, err := readAssertionProfileManifest(t, body); err != nil {
			t.Fatal(err)
		}
		for name, invalid := range map[string]string{
			"sampled rule":              strings.Replace(body, "percent: 100, seed: 7", "percent: 50, seed: 7", 1),
			"missing origin":            strings.Replace(body, "from: reusable-test, ", "", 1),
			"unknown request":           strings.Replace(body, "request: payment-read", "request: unknown", 1),
			"empty requests":            strings.Replace(body, "min_requests: 3", "min_requests: 0", 1),
			"zero limit":                strings.Replace(body, "max_attempts_per_request: 2", "max_attempts_per_request: 0", 1),
			"wrong fault":               strings.Replace(body, "kind: "+map[bool]string{false: "http_status", true: "tcp_reset"}[tcp], "kind: dns_timeout", 1),
			"budget collision":          strings.Replace(body, "assertions: [healthy, attempts]\n", "assertions: [healthy, attempts]\n              "+inline+": {request: payment-read, max_attempts_per_request: 3, min_requests: 1}\n", 1),
			"duplicate budget profiles": strings.Replace(strings.Replace(body, "  healthy:\n", "  other:\n    "+field+": {request: payment-read, max_attempts_per_request: 3, min_requests: 1}\n  healthy:\n", 1), "[healthy, attempts]", "[healthy, attempts, other]", 1),
			"no fault":                  strings.Replace(body, "assertions: [healthy]}", "assertions: [attempts]}", 1),
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				if _, err := readAssertionProfileManifest(t, invalid); err == nil {
					t.Fatal("invalid attempt profile accepted")
				}
			})
		}
	}
	for _, limit := range []float64{math.NaN(), math.Inf(1), -1, 1001} {
		if err := validateTestAssertionProfiles(map[string]testAssertionProfile{"attempts": {DependencyAttempts: &testRetryAmplificationSpec{Request: "read", MinRequests: 1, MaxAttemptsPerRequest: limit}}}); err == nil {
			t.Fatal("invalid unused profile accepted")
		}
	}
}

func TestAssertionAttemptProfilesIsolateBudgetsAndBindReplay(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		manifest, err := readAssertionProfileManifest(t, attemptAssertionManifest(tcp))
		if err != nil {
			t.Fatal(err)
		}
		original := manifest.Scenarios["reusable-test"]
		otherManifest, err := readAssertionProfileManifest(t, attemptAssertionManifest(tcp))
		if err != nil {
			t.Fatal(err)
		}
		before := newTestReplayManifest("reusable-test", "warm", "", original)
		phase := &original.Steps[1].Load.ChaosSchedule[1]
		budget, profile := phase.RetryAmplification, manifest.AssertionProfiles["attempts"].DependencyAttempts
		if tcp {
			budget, profile = phase.ConnectionAttempts, manifest.AssertionProfiles["attempts"].ConnectionAttempts
		}
		budget.MaxAttemptsPerRequest = 4
		if profile.MaxAttemptsPerRequest != 2 {
			t.Fatal("profile definition mutated")
		}
		after := newTestReplayManifest("reusable-test", "warm", "", original)
		if before == nil || after == nil || before.ConfigurationSHA256 == after.ConfigurationSHA256 {
			t.Fatal("attempt limit absent from replay fingerprint")
		}
		copyScenario, err := expandTestAssertionProfiles(testScenario{Steps: []testScenarioStep{{Name: "other", Load: &testLoadSpec{ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "fault", Assertions: []string{"attempts"}}}}}}}, manifest.AssertionProfiles)
		if err != nil {
			t.Fatal(err)
		}
		copied := copyScenario.Steps[0].Load.ChaosSchedule[0].RetryAmplification
		other := otherManifest.Scenarios["reusable-test"].Steps[1].Load.ChaosSchedule[1].RetryAmplification
		if tcp {
			copied = copyScenario.Steps[0].Load.ChaosSchedule[0].ConnectionAttempts
			other = otherManifest.Scenarios["reusable-test"].Steps[1].Load.ChaosSchedule[1].ConnectionAttempts
		}
		if copied.MaxAttemptsPerRequest != 2 || other.MaxAttemptsPerRequest != 2 {
			t.Fatal("budget leaked across uses")
		}
	}
}

func TestAssertionAttemptProfilesMeasureRealRetriesAndReportStorms(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tcp      bool
		attempts int
		missing  bool
	}{
		{name: "HTTP bounded", attempts: 1}, {name: "HTTP retry storm", attempts: 3},
		{name: "TCP bounded", tcp: true, attempts: 1}, {name: "TCP retry storm", tcp: true, attempts: 3},
		{name: "TCP legacy evidence", tcp: true, attempts: 1, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := readAssertionProfileManifest(t, attemptAssertionManifest(tc.tcp))
			if err != nil {
				t.Fatal(err)
			}
			step := manifest.Scenarios["reusable-test"].Steps[1]
			plan, err := (&testChaosSpec{Duration: "1s", Rules: step.Load.ChaosSchedule[1].Rules}).plan()
			if err != nil {
				t.Fatal(err)
			}
			rule := plan.Rules[0]
			var active atomic.Bool
			var attempts atomic.Int64
			dependency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if active.Load() {
					attempts.Add(1)
					w.WriteHeader(503)
				} else {
					w.WriteHeader(200)
				}
			}))
			defer dependency.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					if active.Load() {
						attempts.Add(1)
					} else {
						_, _ = conn.Write([]byte("x"))
					}
					_ = conn.Close()
				}
			}()
			defer func() { _ = listener.Close(); <-done }()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPut:
					attempts.Store(0)
					active.Store(true)
					_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "attempts", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
				case http.MethodDelete:
					active.Store(false)
					w.WriteHeader(http.StatusNoContent)
				case http.MethodGet:
					count := attempts.Load()
					if tc.tcp && count > 0 {
						count = 1
					}
					matches := []api.ScenarioTestChaosMatch{{RuleID: chaos.RuleID(rule), Count: count}}
					if tc.tcp && !tc.missing {
						matches = append(matches, api.ScenarioTestChaosMatch{RuleID: chaos.TCPAttemptID(rule), Count: attempts.Load()})
					}
					_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "attempts", Matches: matches})
				}
			}))
			defer control.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/payment" {
					w.WriteHeader(200)
					return
				}
				count := 1
				if active.Load() {
					count = tc.attempts
				}
				succeeded := false
				for index := 0; index < count; index++ {
					if tc.tcp {
						conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
						if err != nil {
							continue
						}
						_ = conn.SetReadDeadline(time.Now().Add(time.Second))
						buffer := make([]byte, 1)
						_, err = conn.Read(buffer)
						_ = conn.Close()
						succeeded = err == nil
					} else {
						result, err := http.Get(dependency.URL)
						if err != nil {
							continue
						}
						succeeded = result.StatusCode == 200
						_ = result.Body.Close()
					}
					if succeeded {
						break
					}
				}
				if !succeeded {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(200)
				}
			}))
			defer app.Close()
			report, _, runErr := runTestScenarioStepLoad(context.Background(), NewClient(control.URL, "token"), app.URL, "0123456789abcdef0123456789abcdef", nil, step, nil, nil)
			passed := tc.attempts == 1 && !tc.missing
			if (runErr == nil) != passed || report == nil {
				t.Fatalf("report %+v, error %v", report, runErr)
			}
			evidence := report.ChaosPhases[1].RetryAmplification
			if tc.tcp {
				evidence = report.ChaosPhases[1].ConnectionAttempts
			}
			if evidence == nil || evidence.Passed != passed || evidence.Requests < 3 || evidence.MaxAttemptsPerRequest != 2 {
				t.Fatalf("evidence %+v", evidence)
			}
			if !tc.missing && (evidence.Attempts != attempts.Load() || evidence.Attempts <= 0) {
				t.Fatal("actual dependency retries not counted")
			}
			if tc.missing && !strings.Contains(evidence.Error, "connection-attempt support") {
				t.Fatal(evidence.Error)
			}
			var terminal bytes.Buffer
			printTestLoadSummary(&terminal, report)
			if !strings.Contains(terminal.String(), `assertion profile "attempts"`) {
				t.Fatal("terminal omitted profile")
			}
			receipt := testRunReceipt{Scenario: "attempts", Status: report.Status, Load: report}
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
			if (suite.Failures == 0) != passed {
				t.Fatal("JUnit lost verdict")
			}
			var decoded testRunReceipt
			if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil {
				t.Fatal(err)
			}
			decodedEvidence := decoded.Load.ChaosPhases[1].RetryAmplification
			if tc.tcp {
				decodedEvidence = decoded.Load.ChaosPhases[1].ConnectionAttempts
			}
			if decodedEvidence == nil || decodedEvidence.Attempts != evidence.Attempts || decodedEvidence.AttemptsPerRequest != evidence.AttemptsPerRequest || decodedEvidence.Requests != evidence.Requests {
				t.Fatal("JSON lost measurements")
			}
			path = filepath.Join(t.TempDir(), "results.html")
			if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			body, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"Assertion profile: attempts", "attempts /", "limit 2"} {
				if !strings.Contains(html.UnescapeString(string(body)), value) {
					t.Fatalf("HTML omitted %q", value)
				}
			}
		})
	}
}

func TestHTTPDependencyRestartConnectionAssertionProfilesExampleManifest(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-checkout/gregale-connection-assertions-test.yaml", "connection-assertions", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("connection assertion example: %v", err)
	}
	p := prepared[0]
	if _, err := collectTestValidationForEngine(map[string]testScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Steps[1].Load.ChaosSchedule[1].ConnectionAttempts == nil {
		t.Fatal("connection profile missing")
	}
}
