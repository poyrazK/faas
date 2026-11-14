package main_test

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioReportsPreserveInputsBeforeExecution(t *testing.T) {
	for _, suite := range []bool{false, true} {
		for _, input := range []string{"manifest", "data"} {
			for _, flag := range []string{"--report", "--junit", "--html"} {
				for _, alias := range []string{"direct", "symlink", "hardlink"} {
					name := "single/"
					if suite {
						name = "suite/"
					}
					t.Run(name+input+"/"+flag+"/"+alias, func(t *testing.T) {
						dir := t.TempDir()
						main.CaptureSuiteTestOutput(t)
						scenario := main.TestScenario{Project: "audit-app", Command: []string{"sh", "-c", "printf ran > started"}}
						manifest := main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"first": scenario, "second": scenario},
							Suites: map[string]main.TestSuite{"audit": {Scenarios: []main.TestSuiteMember{
								{Scenario: "first", Engine: "local"}, {Scenario: "second", Engine: "local", Data: "cases.json"},
							}}},
						}
						manifestPath := main.WriteSuiteTestManifest(t, dir, manifest)
						dataPath := filepath.Join(dir, "cases.json")
						main.WriteSuiteTestFile(t, dataPath, `[{"label":"preserve-me"}]`)
						inputPath := manifestPath
						if input == "data" {
							inputPath = dataPath
						}
						original, err := os.ReadFile(inputPath)
						if err != nil {
							t.Fatal(err)
						}
						output := inputPath
						if alias != "direct" {
							output = filepath.Join(dir, "output")
							link := os.Symlink
							if alias == "hardlink" {
								link = os.Link
							}
							if err := link(inputPath, output); err != nil {
								t.Fatal(err)
							}
						}
						args := []string{"--manifest", manifestPath, "--base-url", "http://127.0.0.1:1", flag, output}
						if suite {
							args = append(args, "--suite", "audit")
						} else {
							args = append(args, "--scenario", "first", "--engine", "local", "--data", dataPath)
						}
						code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) })
						if code == 0 || !strings.Contains(problem, "would overwrite test input") {
							t.Errorf("report input collision exit %d: %s", code, problem)
						}
						current, err := os.ReadFile(inputPath)
						if err != nil || !bytes.Equal(original, current) {
							t.Errorf("test input changed: %v", err)
						}
						if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
							t.Fatal("a scenario executed before rejecting the input collision")
						}
					})
				}
			}
		}
	}
}

func TestScenarioNonfiniteJSONRejectedBeforeExecution(t *testing.T) {
	for _, value := range []struct {
		name   string
		number float64
	}{{"nan", math.NaN()}, {"positive-infinity", math.Inf(1)}, {"negative-infinity", math.Inf(-1)}} {
		for _, expectation := range []bool{false, true} {
			name := value.name + "/request"
			if expectation {
				name = value.name + "/expectation"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				writeHostedTestApp(t, dir)
				main.CaptureSuiteTestOutput(t)
				request := main.TestHTTPRequest{Name: "create", Method: "POST", Path: "/records", Expect: main.TestHTTPExpect{Status: 200}}
				nested := map[string]any{"private-payload": []any{value.number}}
				if expectation {
					request.Expect.JSON = map[string]any{"/amount": nested}
				} else {
					request.JSON = nested
				}
				path := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{
					"nonfinite": {Project: "audit-app", Requests: []main.TestHTTPRequest{request}},
				}})
				for _, validate := range []bool{false, true} {
					args := []string{"--manifest", path, "--scenario", "nonfinite", "--profile", "warm"}
					if validate {
						args = append(args, "--validate")
					}
					code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) })
					if code == 0 || !strings.Contains(problem, "finite numbers") || strings.Contains(problem, "private-payload") {
						t.Fatalf("nonfinite JSON (validate=%t) exit %d: %s", validate, code, problem)
					}
				}
			})
		}
	}
}

func TestScenarioReplayEncodingFailurePreservesFailedReceipt(t *testing.T) {
	// Bypass manifest validation to exercise the runner's defensive error path.
	// The missing source fails before any platform request can be made.
	dir := t.TempDir()
	plan := main.TestRunPlan{Profile: "warm", Prepared: &main.TestPreparedScenario{
		Name: "nonfinite", Engine: "real-vm", ManifestDir: dir,
		Scenario: main.TestScenario{Source: "missing-source", Requests: []main.TestHTTPRequest{{JSON: math.NaN()}}},
	}}
	receipt := main.ExecuteTestRunPlan(t.Context(), nil, plan)
	if receipt.Status != "failed" || receipt.Replay != nil || !strings.Contains(receipt.Error, "source directory") || !strings.Contains(receipt.Error, "replay configuration could not be encoded") {
		t.Fatalf("failed receipt = %+v", receipt)
	}
	path := filepath.Join(dir, "failure.json")
	if err := main.WriteTestReport(path, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatalf("failed receipt could not be saved: %v", err)
	}
}

func TestNativeTestHTTPJSONNumberEquality(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		expected any
		pass     bool
	}{
		{"decimal", `1.0`, 1, true},
		{"exponent", `1e0`, 1, true},
		{"negative-zero", `-0.0e5`, 0, true},
		{"large-integer", `9007199254740993.0`, int64(9007199254740993), true},
		{"large-distinct-integers", `9007199254740992`, int64(9007199254740993), false},
		{"large-exponent", `1e1000`, json.Number("10e999"), true},
		{"small-exponent", `1e-1000`, json.Number("10e-1001"), true},
		{"different-fractions", `0.10000000000000001`, json.Number("0.1"), false},
		{"nested", `{"items":[1.0,{"amount":2e0}]}`, map[string]any{"items": []any{1, map[string]any{"amount": 2}}}, true},
		{"array-order", `[2.0,1.0]`, []any{1, 2}, false},
		{"string-is-not-number", `"1"`, 1, false},
		{"boolean-is-not-number", `true`, 1, false},
		{"null-is-not-number", `null`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"value":` + tc.body + `}`))
			}))
			defer server.Close()
			steps := []main.TestHTTPRequest{{Name: "read", Method: "GET", Path: "/", Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/value": tc.expected}}}}
			evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "numbers", nil, steps, map[string]string{})
			if (err == nil) != tc.pass || len(evidence) != 1 || evidence[0].Passed != tc.pass {
				t.Fatalf("numeric assertion = %+v, %v; want passed=%t", evidence, err, tc.pass)
			}
		})
	}
}

func stageCaptureScenario() main.TestScenario {
	return main.TestScenario{Project: "audit-app", Steps: []main.TestScenarioStep{
		{Name: "baseline", Requests: []main.TestHTTPRequest{{Name: "create", Method: "POST", Path: "/records", Expect: main.TestHTTPExpect{Status: 201}, Capture: map[string]string{"id": "/id"}}},
			Checks: []main.TestHTTPRequest{{Name: "inspect", Method: "GET", Path: "/records/${steps.create.id}", Expect: main.TestHTTPExpect{Status: 200}, Capture: map[string]string{"state": "/state"}}}},
		{Name: "fault", Chaos: &main.TestChaosSpec{Duration: "1s", Rules: []main.TestChaosRule{{To: "audit-app", Kind: "http_status", Percent: 100, StatusCode: 503}}},
			Requests: []main.TestHTTPRequest{{Name: "verify", Method: "POST", Path: "/records/${steps.create.id}", Headers: map[string]string{"X-State": "${steps.inspect.state}"}, JSON: map[string]any{"state": "${steps.inspect.state}"},
				Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/state": "${steps.inspect.state}"}}, Capture: map[string]string{"fault_id": "/id"}}}},
		{Name: "clear", ClearChaos: true},
		{Name: "recovery", Requests: []main.TestHTTPRequest{{Name: "recover", Method: "GET", Path: "/records/${steps.create.id}", Expect: main.TestHTTPExpect{Status: 200}}}},
	}}
}

func stageCaptureStreams() *main.TestLoadSpec {
	return &main.TestLoadSpec{Duration: "1s", Streams: []main.TestLoadStreamSpec{
		{Name: "writes", Requests: []string{"verify"}, Rate: 1, VUs: 1},
		{Name: "reads", Requests: []string{"independent"}, Rate: 1, VUs: 1},
	}}
}

func TestScenarioStagesShareEarlierCaptures(t *testing.T) {
	for _, mode := range []string{"ordinary", "load", "streams"} {
		t.Run(mode, func(t *testing.T) {
			scenario := stageCaptureScenario()
			if mode == "load" {
				scenario.Steps[1].Load = &main.TestLoadSpec{Iterations: 2, VUs: 1}
			}
			if mode == "streams" {
				scenario.Steps[1].Load = stageCaptureStreams()
				scenario.Steps[1].Requests = append(scenario.Steps[1].Requests, main.TestHTTPRequest{Name: "independent", Method: "GET", Path: "/records/${steps.create.id}", Expect: main.TestHTTPExpect{Status: 200}})
			}
			if err := main.ValidateTestScenarioSteps(scenario, nil); err != nil {
				t.Fatalf("earlier stage captures rejected: %v", err)
			}
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/chaos") {
					t.Errorf("unexpected control request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = w.Write([]byte(`{"generation":"capture-test","rules_installed":1,"matches":[]}`))
			}))
			defer control.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/records" {
					w.WriteHeader(http.StatusCreated)
				} else if r.URL.EscapedPath() != "/records/a%2Fb" {
					t.Errorf("captured path was not expanded and escaped: %s", r.URL)
				}
				if r.Method == http.MethodPost && r.URL.Path != "/records" {
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["state"] != "ready" || r.Header.Get("X-State") != "ready" {
						t.Errorf("cross-stage request = %v, %v, %v", body, r.Header, err)
					}
				}
				_, _ = w.Write([]byte(`{"id":"a/b","state":"ready"}`))
			}))
			defer app.Close()
			captures := map[string]string{}
			receipt := main.TestRunReceipt{}
			if err := main.RunTestScenarioSteps(t.Context(), main.NewClient(control.URL, "token"), t.TempDir(), app.URL, nil, nil, "0123456789abcdef0123456789abcdef", scenario.Steps, captures, &receipt); err != nil {
				t.Fatalf("cross-stage execution: %v", err)
			}
			if len(receipt.Steps) != 4 || captures["steps.create.id"] != "a/b" {
				t.Fatalf("stage evidence/captures = %+v, %v", receipt.Steps, captures)
			}
			if _, exported := captures["steps.verify.fault_id"]; exported != (mode == "ordinary") {
				t.Fatal("load captures escaped their iteration scope")
			}
		})
	}
}

func TestScenarioStagesRejectUnavailableCaptures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*main.TestScenario)
		problem string
	}{
		{"unknown", func(s *main.TestScenario) { s.Steps[1].Requests[0].Path = "/${steps.missing.id}" }, "unknown reference"},
		{"self", func(s *main.TestScenario) { s.Steps[0].Requests[0].Path = "/${steps.create.id}" }, "unknown reference"},
		{"forward-stage", func(s *main.TestScenario) { s.Steps[0].Requests[0].Path = "/${steps.verify.fault_id}" }, "unknown reference"},
		{"load-private", func(s *main.TestScenario) {
			s.Steps[1].Load = &main.TestLoadSpec{Iterations: 1}
			s.Steps[3].Requests[0].Path = "/${steps.verify.fault_id}"
		}, "unknown reference"},
		{"cross-stream", func(s *main.TestScenario) {
			s.Steps[1].Load = stageCaptureStreams()
			s.Steps[1].Requests = append(s.Steps[1].Requests, main.TestHTTPRequest{Name: "independent", Method: "GET", Path: "/${steps.verify.fault_id}", Expect: main.TestHTTPExpect{Status: 200}})
		}, "before it runs in this stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := stageCaptureScenario()
			tc.edit(&scenario)
			if err := main.ValidateTestScenarioSteps(scenario, nil); err == nil || !strings.Contains(err.Error(), tc.problem) {
				t.Fatalf("unavailable capture validation = %v; want %s", err, tc.problem)
			}
		})
	}
}

func TestScenarioRestartUsesOnlySharedStageCaptures(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		for _, load := range []bool{false, true} {
			name := "storage"
			if inFlight {
				name = "in-flight"
			}
			if load {
				name += "/load-private"
			}
			t.Run(name, func(t *testing.T) {
				scenario := stageCaptureScenario()
				// Remove the intervening consumer so the restart validator itself
				// must reject a reference to an earlier load iteration's capture.
				scenario.Steps = scenario.Steps[:1]
				if load {
					scenario.Steps[0].Load = &main.TestLoadSpec{Iterations: 1}
				}
				restart := &main.TestRestartSpec{Workload: "app", Timeout: "1s",
					Readiness:     main.TestHTTPRequest{Name: "ready", Method: "GET", Path: "/ready", Expect: main.TestHTTPExpect{Status: 200}},
					StorageChecks: []main.TestHTTPRequest{{Name: "storage", Method: "GET", Path: "/records", Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/id": "${steps.create.id}"}}}},
				}
				if inFlight {
					restart.StorageChecks[0].Expect.JSON["/id"] = "constant"
					restart.InFlight = &main.TestRestartInFlightSpec{Request: main.TestHTTPRequest{Name: "write", Method: "POST", Path: "/records/${steps.create.id}", JSON: map[string]any{"state": "pending"}, Expect: main.TestHTTPExpect{Status: 200}},
						Checkpoints: []main.TestRestartCheckpointSpec{{Workload: "app", Request: main.TestHTTPRequest{Name: "pending", Method: "GET", Path: "/pending", Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/pending": true}}}}},
					}
				}
				scenario.Steps = append(scenario.Steps, main.TestScenarioStep{Name: "restart", Restart: restart}, main.TestScenarioStep{Name: "recovery", Command: []string{"true"}})
				err := main.ValidateTestScenarioSteps(scenario, nil)
				if (err == nil) == load || (load && !strings.Contains(err.Error(), "unknown reference")) {
					t.Fatalf("restart capture validation (load=%t) = %v", load, err)
				}
			})
		}
	}
}
