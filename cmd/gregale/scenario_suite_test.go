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
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

func writeSuiteTestManifest(t *testing.T, dir string, manifest testManifest) string {
	t.Helper()
	body, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gregale-test.yaml")
	writeSuiteTestFile(t, path, string(body))
	return path
}

func writeSuiteTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func captureSuiteTestOutput(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonOutput, osStdout, osStderr = true, stdout, stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	return stdout, stderr
}

func suiteTestScenario(path string) testScenario {
	return testScenario{Project: "suite-api", Checks: []testHTTPRequest{{Name: "read", Method: "GET", Path: path, Expect: testHTTPExpect{Status: 200}}}}
}

func TestScenarioSuiteRunsInDeclaredOrderWithMemberDataAndCombinedReports(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	t.Setenv("FAAS_TOKEN", "")
	var mu sync.Mutex
	var order []string
	runs := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		run := r.Header.Get("X-Run")
		if run == "" || runs[run] {
			t.Errorf("run identity was empty or reused: %q", run)
		}
		runs[run] = true
		order = append(order, r.URL.Path+":"+r.Header.Get("X-Data"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	first, second := suiteTestScenario("/first"), suiteTestScenario("/second")
	first.Checks[0].Headers = map[string]string{"X-Run": "${run.id}", "X-Data": "${data.customer}"}
	second.Checks[0].Headers = map[string]string{"X-Run": "${run.id}", "X-Data": "${data.label}"}
	for _, scenario := range []*testScenario{&first, &second} {
		scenario.Setup = [][]string{{"sh", "-c", `printf 'setup:%s:%s\n' "$GREGALE_TEST_SCENARIO" "$GREGALE_TEST_CASE" >> order.log`}}
		scenario.Cleanup = [][]string{{"sh", "-c", `printf 'cleanup:%s:%s\n' "$GREGALE_TEST_SCENARIO" "$GREGALE_TEST_CASE" >> order.log`}}
	}
	writeSuiteTestFile(t, filepath.Join(dir, "customers.json"), `[{"customer":"private-first"},{"customer":"private-second"}]`)
	writeSuiteTestFile(t, filepath.Join(dir, "labels.csv"), "label\nprivate-label\n")
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"z-first": first, "a-second": second},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{
			{Scenario: "z-first", Engine: "local", Data: "customers.json"},
			{Scenario: "a-second", Engine: "local", Data: "labels.csv"},
		}}},
	})
	report, junit := filepath.Join(dir, "results.json"), filepath.Join(dir, "results.xml")
	args := []string{"--suite", "smoke", "--manifest", manifest, "--base-url", server.URL, "--repeat", "2", "--report", report, "--junit", junit}
	if code := cmdTest(args); code != 0 {
		t.Fatalf("suite exit %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 6 {
		t.Fatalf("JSON output = %s (%v)", stdout, err)
	}
	var expectedFixtures []string
	for i, receipt := range receipts {
		name, row, attempt := "z-first", i%2+1, i/2+1
		if i >= 4 {
			name, row, attempt = "a-second", 1, i-3
		}
		if receipt.Suite != "smoke" || receipt.Scenario != name || receipt.Engine != "local" || receipt.Profile != "local" || receipt.Case != fmt.Sprintf("row-%d", row) || receipt.Attempt != attempt || receipt.Status != "passed" {
			t.Fatalf("receipt %d = %+v", i, receipt)
		}
		expectedFixtures = append(expectedFixtures, "setup:"+name+":"+receipt.Case, "cleanup:"+name+":"+receipt.Case)
	}
	mu.Lock()
	if !reflect.DeepEqual(order, []string{"/first:private-first", "/first:private-second", "/first:private-first", "/first:private-second", "/second:private-label", "/second:private-label"}) {
		t.Errorf("request order = %v", order)
	}
	mu.Unlock()
	fixtures, err := os.ReadFile(filepath.Join(dir, "order.log"))
	if err != nil || strings.TrimSpace(string(fixtures)) != strings.Join(expectedFixtures, "\n") {
		t.Fatalf("fixtures did not finish between runs: %s (%v)", fixtures, err)
	}
	saved, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var savedReceipts []testRunReceipt
	if err := json.Unmarshal(saved, &savedReceipts); err != nil || !reflect.DeepEqual(savedReceipts, receipts) {
		t.Fatalf("combined JSON report differs: %v", err)
	}
	xmlBody, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	var xmlSuite testJUnitSuite
	if err := xml.Unmarshal(xmlBody, &xmlSuite); err != nil || xmlSuite.Name != "gregale suite smoke" || xmlSuite.Tests != 6 || xmlSuite.Failures != 0 || xmlSuite.Skipped != 0 {
		t.Fatalf("combined JUnit = %+v (%v)", xmlSuite, err)
	}
	for _, body := range []string{stdout.String(), string(saved), string(xmlBody)} {
		if strings.Contains(body, "private-") {
			t.Fatal("suite report included case values")
		}
	}
}

func TestScenarioSuiteFailureContinuesOrSkipsAfterCleanup(t *testing.T) {
	for _, failFast := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail-fast=%t", failFast), func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr := captureSuiteTestOutput(t)
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				paths = append(paths, r.URL.Path)
				if r.URL.Path == "/fail" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			first := suiteTestScenario("/fail")
			first.Cleanup = [][]string{{"sh", "-c", "echo cleaned > cleanup.log"}}
			second := suiteTestScenario("/pass")
			second.Setup = [][]string{{"sh", "-c", "test -f cleanup.log"}}
			manifest := writeSuiteTestManifest(t, dir, testManifest{
				Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
				Suites: map[string]testSuite{"regression": {Scenarios: []testSuiteMember{{Scenario: "first"}, {Scenario: "second"}}}},
			})
			junit := filepath.Join(dir, "results.xml")
			args := []string{"--suite", "regression", "--engine", "local", "--base-url", server.URL, "--manifest", manifest, "--junit", junit}
			if failFast {
				args = append(args, "--fail-fast")
			}
			if code := cmdTest(args); code != 1 {
				t.Fatalf("failed suite exit %d: %s", code, stderr)
			}
			var receipts []testRunReceipt
			if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 || receipts[0].Status != "failed" {
				t.Fatalf("receipts = %s (%v)", stdout, err)
			}
			if body, err := os.ReadFile(filepath.Join(dir, "cleanup.log")); err != nil || string(body) != "cleaned\n" {
				t.Fatalf("cleanup did not finish: %s (%v)", body, err)
			}
			skipped := 0
			mu.Lock()
			defer mu.Unlock()
			if failFast {
				skipped = 1
				if receipts[1].Status != "skipped" || !strings.Contains(receipts[1].SkipReason, "--fail-fast") || receipts[1].RunID != "" || len(receipts[1].Requests) != 0 || !reflect.DeepEqual(paths, []string{"/fail"}) {
					t.Fatalf("skipped run executed: receipts=%+v, paths=%v", receipts, paths)
				}
			} else if receipts[1].Status != "passed" || !reflect.DeepEqual(paths, []string{"/fail", "/pass"}) {
				t.Fatalf("suite did not continue: receipts=%+v, paths=%v", receipts, paths)
			}
			body, err := os.ReadFile(junit)
			if err != nil {
				t.Fatal(err)
			}
			var report testJUnitSuite
			if err := xml.Unmarshal(body, &report); err != nil || report.Tests != 2 || report.Failures != 1 || report.Skipped != skipped {
				t.Fatalf("failure/skip counts = %+v (%v)", report, err)
			}
			if failFast && (report.Cases[1].Skipped == nil || report.Cases[1].Failure != nil) {
				t.Fatalf("JUnit skipped case = %+v", report.Cases[1])
			}
		})
	}
}

func TestScenarioSuiteValidatesEveryMemberBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	first := suiteTestScenario("/first")
	first.Command = []string{"sh", "-c", "touch executed"}
	first.Checks = nil
	second := suiteTestScenario("/second")
	second.Checks[0].Headers = map[string]string{"X-Customer": "${data.customer}"}
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "first", Engine: "local"}, {Scenario: "second", Engine: "local", Data: "missing.json"}}}},
	})
	code, problem := runWithStderr(t, func() int {
		return cmdTest([]string{"--suite", "smoke", "--manifest", manifest, "--base-url", "http://localhost:3000"})
	})
	if code != 1 || !strings.Contains(problem, "case data") || strings.Contains(problem, "transport_error") {
		t.Fatalf("missing late-member data exit %d: %s / %s", code, stdout, problem)
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatalf("first scenario executed before suite validation: %v", err)
	}
	writeSuiteTestFile(t, filepath.Join(dir, "missing.json"), `[{"wrong":"value"}]`)
	stderr.Reset()
	code, problem = runWithStderr(t, func() int {
		return cmdTest([]string{"--suite", "smoke", "--validate", "--manifest", manifest})
	})
	if code != 1 || !strings.Contains(problem, "customer") {
		t.Fatalf("incorrect fields exit %d: %s", code, problem)
	}
	writeSuiteTestFile(t, filepath.Join(dir, "missing.json"), `[{"customer":"private-customer"}]`)
	stdout.Reset()
	stderr.Reset()
	if code := cmdTest([]string{"--suite", "smoke", "--validate", "--manifest", manifest}); code != 0 {
		t.Fatalf("offline suite validation exit %d: %s", code, stderr)
	}
	var validation []testValidationResult
	if err := json.Unmarshal(stdout.Bytes(), &validation); err != nil || len(validation) != 2 || validation[0].Suite != "smoke" || validation[0].Engine != "local" || validation[1].Scenario != "second" {
		t.Fatalf("suite validation = %s (%v)", stdout, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatalf("validation executed scenario commands: %v", err)
	}
}

func TestScenarioSuiteRejectsInvalidDeclarationsAndSelections(t *testing.T) {
	stdout, stderr := captureSuiteTestOutput(t)
	for _, tc := range []struct {
		name    string
		suite   string
		members []testSuiteMember
		args    []string
		problem string
	}{
		{"empty", "smoke", nil, nil, "between 1 and 100"},
		{"unknown member", "smoke", []testSuiteMember{{Scenario: "unknown"}}, nil, "unknown scenario"},
		{"duplicate", "smoke", []testSuiteMember{{Scenario: "first"}, {Scenario: "first"}}, nil, "repeats scenario"},
		{"invalid name", "bad suite", []testSuiteMember{{Scenario: "first"}}, nil, "needs a name"},
		{"bad engine", "smoke", []testSuiteMember{{Scenario: "first", Engine: "typo"}}, nil, "invalid engine"},
		{"bad profile", "smoke", []testSuiteMember{{Scenario: "first", Profile: "hot"}}, nil, "invalid"},
		{"local profile", "smoke", []testSuiteMember{{Scenario: "first", Engine: "local", Profile: "warm"}}, nil, "profile applies only"},
		{"global data", "smoke", []testSuiteMember{{Scenario: "first"}}, []string{"--data", "cases.json"}, "declare data on suite members"},
		{"two selections", "smoke", []testSuiteMember{{Scenario: "first"}}, []string{"--scenario", "first"}, "mutually exclusive"},
		{"unknown suite", "other", []testSuiteMember{{Scenario: "first"}}, nil, "not declared"},
		{"local preflight", "smoke", []testSuiteMember{{Scenario: "first", Engine: "local"}}, []string{"--preflight"}, "requires every suite member"},
		{"unused profile", "smoke", []testSuiteMember{{Scenario: "first", Engine: "local"}}, []string{"--profile", "warm"}, "--profile applies only"},
		{"unused cost guard", "smoke", []testSuiteMember{{Scenario: "first", Engine: "local"}}, []string{"--max-workload-minutes", "1"}, "--max-workload-minutes applies only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stdout.Reset()
			stderr.Reset()
			manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"first": suiteTestScenario("/first")}, Suites: map[string]testSuite{tc.suite: {Scenarios: tc.members}}})
			args := []string{"--suite", "smoke", "--manifest", manifest, "--base-url", "http://localhost:3000"}
			args = append(args, tc.args...)
			code, problem := runWithStderr(t, func() int { return cmdTest(args) })
			if code != 1 || !strings.Contains(problem, tc.problem) {
				t.Fatalf("exit %d, want %q in %s", code, tc.problem, problem)
			}
		})
	}
}

func TestScenarioSuiteResolvesEngineOverridesProfilesAndTotalCostGuard(t *testing.T) {
	dir := t.TempDir()
	writeSuiteTestFile(t, filepath.Join(dir, "handler.js"), "exports.handler = async () => ({statusCode: 200});\n")
	first, second := suiteTestScenario("/first"), suiteTestScenario("/second")
	first.Simulation, second.Simulation = []string{"true"}, []string{"true"}
	first.Timeout, second.Timeout = "2m", "3m"
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "first", Engine: "real-vm", Profile: "restored"}, {Scenario: "second", Engine: "real-vm", Profile: "warm"}}}},
	})
	options := testSuiteOptions{Engine: "real-vm", Profile: "all", Repeat: 2}
	prepared, err := prepareTestSuite(manifest, "smoke", options)
	if err != nil || len(prepared) != 2 || !reflect.DeepEqual(prepared[0].Profiles, []string{"restored"}) || !reflect.DeepEqual(prepared[1].Profiles, []string{"warm"}) || estimateTestSuiteWorkloadMinutes(prepared, 2) != 10 {
		t.Fatalf("member profiles/cost = %+v (%v)", prepared, err)
	}
	options.MaxWorkloadMinutes = 6
	if _, err := prepareTestSuite(manifest, "smoke", options); err == nil || !strings.Contains(err.Error(), "up to 10 workload-minutes") {
		t.Fatalf("per-scenario limits bypassed total suite guard: %v", err)
	}
	options.MaxWorkloadMinutes, options.Profile, options.ProfileSet = 0, "cold", true
	prepared, err = prepareTestSuite(manifest, "smoke", options)
	if err != nil || !reflect.DeepEqual(prepared[0].Profiles, []string{"cold"}) || !reflect.DeepEqual(prepared[1].Profiles, []string{"cold"}) {
		t.Fatalf("global profile override = %+v (%v)", prepared, err)
	}
	options.ProfileSet, options.Engine, options.EngineSet = false, "simulated", true
	prepared, err = prepareTestSuite(manifest, "smoke", options)
	if err != nil || prepared[0].Engine != "simulated" || prepared[1].Engine != "simulated" || estimateTestSuiteWorkloadMinutes(prepared, 2) != 0 {
		t.Fatalf("explicit engine override = %+v (%v)", prepared, err)
	}
	stdout, stderr := captureSuiteTestOutput(t)
	if code := cmdTest([]string{"--suite", "smoke", "--manifest", manifest, "--engine", "simulated"}); code != 0 {
		t.Fatalf("simulated suite exit %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 || receipts[0].Engine != "simulated" || receipts[0].Profile != "simulated" || receipts[1].Status != "passed" {
		t.Fatalf("simulated receipts = %s (%v)", stdout, err)
	}
}

func TestScenarioSuiteInterruptedRunsRemainExplicitlySkipped(t *testing.T) {
	stdout, _ := captureSuiteTestOutput(t)
	prepared := []testPreparedScenario{{Name: "export", Engine: "real-vm", Profiles: []string{"warm", "cold", "restored"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := runPreparedTestPlans(ctx, nil, "lifecycle", prepareTestRunPlans(prepared, 2), 2, false)
	if len(results) != 6 {
		t.Fatalf("interrupted run count = %d", len(results))
	}
	for i, receipt := range results {
		if receipt.Status != "skipped" || !strings.Contains(receipt.SkipReason, "interrupted") || receipt.Attempt != i/3+1 || receipt.Suite != "lifecycle" || receipt.AppSlug != "" {
			t.Fatalf("interrupted receipt = %+v", receipt)
		}
	}
	if code := finishTestRunReports("lifecycle", results, "", ""); code != 1 || !strings.Contains(stdout.String(), `"status": "skipped"`) {
		t.Fatalf("interrupted suite incorrectly passed: exit %d, %s", code, stdout)
	}
}

func TestScenarioSuiteFailFastIncludesCleanupFailures(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	first := testScenario{Project: "suite-api", Command: []string{"true"}, Cleanup: [][]string{{"sh", "-c", "exit 7"}}}
	second := testScenario{Project: "suite-api", Command: []string{"sh", "-c", "touch executed"}}
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "first"}, {Scenario: "second"}}}},
	})
	if code := cmdTest([]string{"--suite", "smoke", "--engine", "local", "--manifest", manifest, "--base-url", "http://localhost:3000", "--fail-fast"}); code != 1 {
		t.Fatalf("cleanup failure exit %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 || receipts[0].Status != "failed" || receipts[0].CleanupError == "" || receipts[1].Status != "skipped" {
		t.Fatalf("cleanup failure receipts = %s (%v)", stdout, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatalf("suite continued after cleanup failure: %v", err)
	}
}

func TestScenarioSuiteLoadUsesEachMembersConfiguration(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	first, second := suiteTestScenario("/first"), suiteTestScenario("/second")
	first.Load, second.Load = &testLoadSpec{VUs: 1, Iterations: 2}, &testLoadSpec{VUs: 1, Iterations: 3}
	manifest := writeSuiteTestManifest(t, dir, testManifest{
		Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
		Suites: map[string]testSuite{"smoke": {Scenarios: []testSuiteMember{{Scenario: "first", Engine: "local"}, {Scenario: "second", Engine: "local"}}}},
	})
	if code := cmdTest([]string{"--suite", "smoke", "--manifest", manifest, "--base-url", server.URL, "--load"}); code != 0 {
		t.Fatalf("suite load exit %d: %s", code, stderr)
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 2 {
		t.Fatalf("load receipts = %s (%v)", stdout, err)
	}
	for i, receipt := range receipts {
		if receipt.Load == nil || receipt.Status != "passed" || receipt.Load.IterationLimit != i+2 || receipt.Load.IterationsCompleted != i+2 || receipt.Suite != "smoke" {
			t.Fatalf("member load settings were lost: %+v", receipt)
		}
	}
}

type suitePreflightTestClient struct {
	account      api.AccountResponse
	accountCalls int
	capCalls     int
}

func (c *suitePreflightTestClient) Whoami(context.Context) (api.AccountResponse, error) {
	c.accountCalls++
	return c.account, nil
}

func (c *suitePreflightTestClient) GetCapabilities(context.Context) (api.CapabilitiesResponse, error) {
	c.capCalls++
	return api.CapabilitiesResponse{}, nil
}

func TestScenarioSuitePreflightChecksSequentialCapacityWithoutProvisioning(t *testing.T) {
	stdout, stderr := captureSuiteTestOutput(t)
	client := &suitePreflightTestClient{account: api.AccountResponse{Plan: "pro", Limits: api.AccountLimits{DeveloperApps: 2}}}
	scenarios := []testPreparedScenario{
		{Name: "first", Engine: "real-vm", Profiles: []string{"restored"}, Scenario: testScenario{Timeout: "2m", Services: map[string]testService{"worker": {}}}},
		{Name: "second", Engine: "real-vm", Profiles: []string{"warm"}, Scenario: testScenario{Timeout: "3m", Services: map[string]testService{"worker": {}}}},
	}
	if code := runTestSuitePreflight(context.Background(), client, scenarios, 2); code != 0 {
		t.Fatalf("sequential capacity was summed as concurrent: exit %d, %s", code, stderr)
	}
	var results []testPreflightResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil || len(results) != 2 || !results[0].Ready || !results[1].Ready || results[0].MaxWorkloadMinutes+results[1].MaxWorkloadMinutes != 20 || client.accountCalls != 1 || client.capCalls != 1 {
		t.Fatalf("suite preflight = %s (%v), calls %d/%d", stdout, err, client.accountCalls, client.capCalls)
	}
	client.account.DeveloperAppCount = 1
	stdout.Reset()
	if code := runTestSuitePreflight(context.Background(), client, scenarios, 2); code != 1 {
		t.Fatalf("insufficient concurrent capacity passed: exit %d", code)
	}
}

func TestScenarioSuiteSkippedLoadRetainsJourneyIdentity(t *testing.T) {
	captureSuiteTestOutput(t)
	prepared := []testPreparedScenario{{
		Name: "export", Engine: "local", Cases: []testDataCase{{Name: "row-1"}},
		Load: &testLoadConfig{VUs: 1, Iterations: 2, RequestLimit: 10},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := runPreparedTestPlans(ctx, nil, "smoke", prepareTestRunPlans(prepared, 1), 1, false)
	if len(results) != 1 || results[0].Status != "skipped" || results[0].Load == nil || results[0].Load.Status != "not_started" || results[0].Load.IterationsStarted != 0 {
		t.Fatalf("skipped load evidence = %+v", results)
	}
	path := filepath.Join(t.TempDir(), "skipped.xml")
	if err := writeTestJUnit(path, results); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report testJUnitSuite
	if err := xml.Unmarshal(body, &report); err != nil || report.Skipped != 1 || report.Failures != 0 || report.Cases[0].Name != "export/local/load/row-1#1" {
		t.Fatalf("skipped load JUnit = %+v (%v)", report, err)
	}
}
