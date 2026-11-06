package main_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioSimulatedValidationAndCIUseCommandSources(t *testing.T) {
	dir := t.TempDir()
	main.CaptureSuiteTestOutput(t)
	scenario := main.TestScenario{Project: "audit-app", Command: []string{"sh", "-c", "exit 0"}, Simulation: []string{"sh", "-c", "exit 0"},
		Services: map[string]main.TestService{"worker": {Source: "unused-hosted-service"}},
	}
	path := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1,
		Scenarios: map[string]main.TestScenario{"audit": scenario},
		Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: "simulated"}}}},
	})
	for _, suite := range []bool{false, true} {
		for _, validate := range []bool{false, true} {
			args := []string{"--manifest", path, "--engine", "simulated"}
			if suite {
				args = append(args, "--suite", "smoke")
			} else {
				args = append(args, "--scenario", "audit")
			}
			if validate {
				args = append(args, "--validate")
			}
			if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code != 0 {
				t.Fatalf("simulation (suite=%t, validate=%t) exit %d: %s", suite, validate, code, problem)
			}
		}
	}
	t.Chdir(dir)
	if code, problem := main.RunWithStderr(t, func() int {
		return main.CmdTest([]string{"ci", "init", "--manifest", path, "--suite", "smoke", "--engine", "simulated"})
	}); code != 0 {
		t.Fatalf("simulation CI generation exit %d: %s", code, problem)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "gregale-test.yml")); err != nil {
		t.Fatalf("simulation workflow was not generated: %v", err)
	}
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{"audit": scenario}, dir, "audit", "real-vm"); err == nil {
		t.Fatal("real-vm validation accepted a source without a deployable app")
	}
	scenario.Simulation = nil
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{"audit": scenario}, dir, "audit", "simulated"); err == nil || !strings.Contains(err.Error(), "no simulation command") {
		t.Fatalf("missing simulation validation = %v", err)
	}
	scenario.Simulation = []string{"true"}
	scenario.Source = "missing-source"
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{"audit": scenario}, dir, "audit", "simulated"); err == nil {
		t.Fatal("simulation validation accepted a missing command directory")
	}
}

func reorderedStreamScenario() main.TestScenario {
	return main.TestScenario{Project: "audit-app", Requests: []main.TestHTTPRequest{
		{Name: "follow", Method: "GET", Path: "/records/${steps.create.id}", Expect: main.TestHTTPExpect{Status: 200}},
		{Name: "create", Method: "POST", Path: "/records", Expect: main.TestHTTPExpect{Status: 201}, Capture: map[string]string{"id": "/id"}},
		{Name: "health", Method: "GET", Path: "/health", Expect: main.TestHTTPExpect{Status: 200}},
	}, Load: &main.TestLoadSpec{Duration: "1s", Streams: []main.TestLoadStreamSpec{
		{Name: "writes", Requests: []string{"create", "follow"}, Rate: 1, VUs: 1},
		{Name: "reads", Requests: []string{"health"}, Rate: 1, VUs: 1},
	}}}
}

func TestScenarioLoadStreamsValidateConfiguredJourneyOrder(t *testing.T) {
	for _, kind := range []string{"requests", "checks"} {
		t.Run(kind, func(t *testing.T) {
			main.CaptureSuiteTestOutput(t)
			scenario := reorderedStreamScenario()
			if kind == "checks" {
				scenario.Checks, scenario.Requests = scenario.Requests, nil
			}
			path := main.WriteSuiteTestManifest(t, t.TempDir(), main.TestManifest{Version: 1,
				Scenarios: map[string]main.TestScenario{"audit": scenario},
				Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: "local"}}}},
			})
			for _, selection := range [][]string{{"--scenario", "audit"}, {"--suite", "smoke"}} {
				args := append([]string{"--manifest", path, "--engine", "local", "--load", "--validate"}, selection...)
				if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code != 0 {
					t.Fatalf("configured stream order exit %d: %s", code, problem)
				}
				args = append([]string{"--manifest", path, "--engine", "local", "--validate"}, selection...)
				if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code == 0 || !strings.Contains(problem, "unknown reference") {
					t.Fatalf("ordinary journey accepted a forward reference: exit %d: %s", code, problem)
				}
			}
		})
	}
	for _, kind := range []string{"forward", "cross-stream", "unassigned", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			scenario := reorderedStreamScenario()
			switch kind {
			case "forward":
				scenario.Load.Streams[0].Requests = []string{"follow", "create"}
			case "cross-stream":
				scenario.Load.Streams[0].Requests = []string{"create"}
				scenario.Load.Streams[1].Requests = []string{"follow", "health"}
			case "unassigned":
				scenario.Load.Streams[0].Requests = []string{"create"}
			case "unknown":
				scenario.Requests[0].Path = "/${steps.missing.id}"
			}
			if err := main.ValidateTestHTTPRequests(scenario); err == nil {
				t.Fatal("invalid stream capture scope was accepted")
			}
		})
	}
}

func TestScenarioStagedStreamsValidateAndExecuteConfiguredOrder(t *testing.T) {
	scenario := stageCaptureScenario()
	stream := reorderedStreamScenario()
	// Keep the earlier ordinary stage's captures available inside each stream.
	stream.Requests[2].Path = "/records/${steps.create.id}"
	stream.Requests[1].Name = "stream-create"
	stream.Requests[1].Capture = map[string]string{"stream_id": "/id"}
	stream.Requests[0].Path = "/records/${steps.stream-create.stream_id}"
	stream.Load.Streams[0].Requests[0] = "stream-create"
	scenario.Steps[1].Requests, scenario.Steps[1].Checks, scenario.Steps[1].Load = stream.Requests, nil, stream.Load
	if err := main.ValidateTestScenarioSteps(scenario, nil); err != nil {
		t.Fatalf("staged stream order: %v", err)
	}
	var creates, follows atomic.Int64
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/records":
			creates.Add(1)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"fresh"}`))
		case r.URL.Path == "/records/fresh":
			follows.Add(1)
		case r.URL.Path != "/records/earlier":
			t.Errorf("unexpected stream request: %s %s", r.Method, r.URL)
		}
	}))
	defer app.Close()
	captures := map[string]string{"steps.create.id": "earlier"}
	report, _, err := main.RunTestScenarioStepLoad(t.Context(), nil, app.URL, "run", nil, scenario.Steps[1], captures, nil)
	if err != nil || report.Status != "passed" || creates.Load() != 1 || follows.Load() != 1 || len(captures) != 1 {
		t.Fatalf("staged stream run = %+v, %v; creates=%d, follows=%d, captures=%v", report, err, creates.Load(), follows.Load(), captures)
	}
}

func TestNativeTestHTTPRejectsCaseDuplicateHeadersBeforeSending(t *testing.T) {
	var sent atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent.Add(1) }))
	defer server.Close()
	for _, name := range []string{"X-Role", "Authorization", "Content-Type"} {
		t.Run(name, func(t *testing.T) {
			step := main.TestHTTPRequest{Name: "read", Method: "GET", Path: "/", Headers: map[string]string{name: "private-first", strings.ToLower(name): "private-second"}, Expect: main.TestHTTPExpect{Status: 200}}
			if err := main.ValidateTestHTTPRequests(main.TestScenario{Requests: []main.TestHTTPRequest{step}}); err == nil || !strings.Contains(err.Error(), "duplicate HTTP header") {
				t.Errorf("duplicate header validation = %v", err)
			}
			_, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, map[string]string{})
			if err == nil || !strings.Contains(err.Error(), "duplicate HTTP header") || strings.Contains(err.Error(), "private-") {
				t.Errorf("duplicate header execution = %v", err)
			}
		})
	}
	if sent.Load() != 0 {
		t.Fatal("duplicate headers reached the HTTP server")
	}
}

func TestNativeTestHTTPArrayPointersRequireCanonicalIndices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[7,8],"object":{"01":"leading","+0":"plus","-0":"negative","":"empty","~1":"tilde"}}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		pointer  string
		expected any
		pass     bool
	}{
		{"/items/0", 7, true}, {"/items/1", 8, true},
		{"/items/01", 8, false}, {"/items/00", 7, false}, {"/items/+0", 7, false}, {"/items/-0", 7, false},
		{"/items/-1", 7, false}, {"/items/", 7, false}, {"/items/-", 7, false}, {"/items/999999999999999999999", 7, false},
		{"/object/01", "leading", true}, {"/object/+0", "plus", true}, {"/object/-0", "negative", true}, {"/object/", "empty", true}, {"/object/~01", "tilde", true},
	} {
		for _, capture := range []bool{false, true} {
			t.Run(tc.pointer+map[bool]string{true: "/capture", false: "/expectation"}[capture], func(t *testing.T) {
				step := main.TestHTTPRequest{Name: "read", Method: "GET", Path: "/", Expect: main.TestHTTPExpect{Status: 200}}
				if capture {
					step.Capture = map[string]string{"value": tc.pointer}
				} else {
					step.Expect.JSON = map[string]any{tc.pointer: tc.expected}
				}
				captures := map[string]string{}
				evidence, err := main.RunTestHTTPRequests(t.Context(), server.URL, "run", nil, []main.TestHTTPRequest{step}, captures)
				if (err == nil) != tc.pass || len(evidence) != 1 || evidence[0].Passed != tc.pass || (!tc.pass && len(captures) != 0) {
					t.Fatalf("pointer result = %+v, %v, captures=%v; want passed=%t", evidence, err, captures, tc.pass)
				}
			})
		}
	}
}

func TestScenarioDataBoundsEncodedRowSize(t *testing.T) {
	const rowLimit = 64 << 10
	for _, format := range []string{"json", "csv"} {
		for _, tc := range []struct {
			name, value string
			pass        bool
		}{
			{"at-limit", strings.Repeat("x", rowLimit-len(`{"payload":""}`)), true},
			{"over-limit", strings.Repeat("x", rowLimit-len(`{"payload":""}`)+1), false},
			{"json-escaping", strings.Repeat("<", 20_000), false},
			{"large-private-value", strings.Repeat("private-value", 16_000), false},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "cases."+format)
				var body strings.Builder
				if format == "json" {
					encoded, err := json.Marshal([]map[string]string{{"payload": tc.value}})
					if err != nil {
						t.Fatal(err)
					}
					body.Write(encoded)
				} else {
					writer := csv.NewWriter(&body)
					if err := writer.WriteAll([][]string{{"payload"}, {tc.value}}); err != nil {
						t.Fatal(err)
					}
				}
				main.WriteSuiteTestFile(t, path, body.String())
				cases, _, err := main.ReadTestData(path)
				if (err == nil) != tc.pass {
					t.Fatalf("encoded row validation = %v; want accepted=%t", err, tc.pass)
				}
				if !tc.pass {
					if !strings.Contains(err.Error(), "row 1") || !strings.Contains(err.Error(), "64 KiB") || strings.Contains(err.Error(), "private-value") {
						t.Fatalf("row error omitted its bound or exposed data: %v", err)
					}
					return
				}
				main.CaptureSuiteTestOutput(t)
				receipt := main.RunLocalTest(t.Context(), "row-boundary", main.TestScenario{Command: []string{"sh", "-c", "test ${#GREGALE_TEST_DATA_JSON} -eq 65536"}}, filepath.Dir(path), "http://127.0.0.1:1", cases[0])
				if receipt.Status != "passed" {
					t.Fatalf("accepted boundary row cannot launch a command: %s", receipt.Error)
				}
			})
		}
	}
}

func TestScenarioOversizedDataRejectedBeforeAnySuiteMember(t *testing.T) {
	dir := t.TempDir()
	main.CaptureSuiteTestOutput(t)
	main.WriteSuiteTestFile(t, filepath.Join(dir, "cases.json"), `[{"payload":"`+strings.Repeat("x", 200_000)+`"}]`)
	scenario := main.TestScenario{Project: "audit-app", Command: []string{"sh", "-c", "printf ran > started"}}
	path := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1,
		Scenarios: map[string]main.TestScenario{"first": scenario, "second": scenario},
		Suites: map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{
			{Scenario: "first", Engine: "local"}, {Scenario: "second", Engine: "local", Data: "cases.json"},
		}}},
	})
	for _, validate := range []bool{false, true} {
		args := []string{"--manifest", path, "--suite", "smoke", "--base-url", "http://127.0.0.1:1"}
		if validate {
			args = append(args, "--validate")
		}
		if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code == 0 || !strings.Contains(problem, "64 KiB") {
			t.Errorf("oversized suite data (validate=%t) exit %d: %s", validate, code, problem)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
		t.Fatal("an earlier suite member ran before rejecting oversized data")
	}
	receipt := main.RunLocalTest(t.Context(), "bypass", scenario, dir, "http://127.0.0.1:1", main.TestDataCase{Values: map[string]any{"payload": strings.Repeat("x", 200_000)}})
	if receipt.Status != "failed" || !strings.Contains(receipt.Error, "64 KiB") {
		t.Fatalf("oversized programmatic data = %+v", receipt)
	}
}

func TestScenarioDataFileAcceptsMultipleBoundedRows(t *testing.T) {
	rows := make([]map[string]string, 8)
	for i := range rows {
		rows[i] = map[string]string{"payload": strings.Repeat("x", 60_000)}
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	main.WriteSuiteTestFile(t, path, string(body))
	cases, _, err := main.ReadTestData(path)
	if err != nil || len(cases) != len(rows) {
		t.Fatalf("bounded rows in a larger data file = %d cases, %v", len(cases), err)
	}
}
