package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func coverageGateFixture(t *testing.T, exclusions string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"definition.yaml": "name: flow\nsteps:\n  - name: send\n    run: send\n    when: {ref: input.active, op: eq, value: true}\n",
		"suite.yaml":      "definition: definition.yaml\n" + exclusions + "scenarios:\n  - name: matched\n    expect:\n      send: {state: mocked}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "suite.yaml")
}

func TestAutomationCoverageExclusionValidation(t *testing.T) {
	for _, body := range []string{
		"  - {step: missing, code: guard_skip_missing, reason: deliberate}\n",
		"  - {step: send, code: typo, reason: deliberate}\n",
		"  - {step: send, code: retry_missing, reason: deliberate}\n",
		"  - {step: send, code: guard_skip_missing, reason: ''}\n",
		"  - {step: send, code: guard_skip_missing, reason: '   '}\n",
		"  - {step: send, code: guard_skip_missing, reason: \"two\\nlines\"}\n",
		"  - {step: send, code: guard_skip_missing, reason: deliberate, unknown: true}\n",
		"  - {step: send, code: guard_skip_missing, reason: one}\n  - {step: send, code: guard_skip_missing, reason: two}\n",
	} {
		suite := coverageGateFixture(t, "coverage_exclusions:\n"+body)
		if _, err := loadAutomationScenarios(suite); err == nil {
			t.Fatalf("accepted exclusion %s", body)
		}
	}
	suite := coverageGateFixture(t, "coverage_exclusions:\n  - {step: send, code: guard_skip_missing, reason: 'handled by an external integration test'}\n")
	if scenarios, err := loadAutomationScenarios(suite); err != nil || len(scenarios[0].coverageExclusions) != 1 {
		t.Fatalf("scenarios=%+v err=%v", scenarios, err)
	}
}

func TestAutomationCoverageGateCheck(t *testing.T) {
	for _, mode := range []string{"advisory", "blocked", "excluded", "assertion_failure", "suggest"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			output := captureAutomationStdout(t)
			exclusions := ""
			if mode == "excluded" || mode == "assertion_failure" {
				exclusions = "coverage_exclusions:\n  - {step: send, code: guard_skip_missing, reason: external integration test}\n"
			}
			suite := coverageGateFixture(t, exclusions)
			state := "mocked"
			if mode == "assertion_failure" {
				state = "skipped"
			}
			body := `{"definition_valid":true,"complete":true,"issues":[],"trace":[{"step_name":"send","state":"` + state + `","when_matched":true}]}`
			authedFakeAPI(t, body, 200)
			args := []string{"--app", "billing", "--scenarios", suite}
			if mode != "advisory" {
				args = append(args, "--require-coverage")
			}
			if mode == "suggest" {
				args = append(args, "--suggest")
			}
			code := cmdAutomationsCheck(args)
			passed := mode == "advisory" || mode == "excluded"
			if (code == 0) != passed {
				t.Fatalf("exit=%d output=%s", code, output)
			}
			var report automationCheckReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Passed != passed {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if mode == "advisory" {
				if report.Coverage != nil {
					t.Fatal("enabled coverage gate by default")
				}
				return
			}
			if report.Coverage == nil || !report.Coverage.Required || report.Coverage.Passed != (mode == "excluded") {
				t.Fatal(report)
			}
			if mode == "excluded" && (report.Coverage.Remaining != 0 || len(report.Coverage.Exclusions) != 1 || !report.Scenarios[0].Passed) {
				t.Fatal(report)
			}
			if mode == "suggest" && report.Suggestions == nil {
				t.Fatal("gate failure prevented writing suggestions")
			}
		})
	}
}

func TestAutomationCoverageGatePublishing(t *testing.T) {
	for _, mode := range []string{"advisory", "blocked", "excluded", "no_scenarios", "invalid_exclusion"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = true
			output := captureAutomationStdout(t)
			exclusions := ""
			if mode == "excluded" {
				exclusions = "coverage_exclusions:\n  - {step: send, code: guard_skip_missing, reason: external integration test}\n"
			}
			if mode == "invalid_exclusion" {
				exclusions = "coverage_exclusions:\n  - {step: send, code: typo, reason: external test}\n"
			}
			suite := coverageGateFixture(t, exclusions)
			definition, ok := readAutomationDefinition(filepath.Join(filepath.Dir(suite), "definition.yaml"))
			if !ok {
				t.Fatal("definition")
			}
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, ":publish-policy") {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(api.AutomationPublishPolicy{Mode: "optional"})
					return
				}

				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, ":simulate") {
					yes := true
					_ = json.NewEncoder(w).Encode(api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{{StepName: "send", State: "mocked", WhenMatched: &yes}}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/publish-check") {
					raw, _ := json.Marshal(definition)
					evidence := api.AutomationCheckEvidence{ServerVerified: true, DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(raw)), CheckedVersion: 7, CheckedAt: time.Now(), Scenarios: []api.AutomationCheckScenario{{Name: "matched", Passed: true, DefinitionValid: true, Complete: true}}, Exclusions: []api.AutomationCheckExclusion{}, CoveragePassed: true}
					_ = json.NewEncoder(w).Encode(api.CheckAutomationPublicationResponse{Receipt: "server-receipt", ExpiresAt: time.Now().Add(time.Minute), Evidence: evidence})
					return
				}
				version := int64(7)
				if r.Method == "POST" {
					version = 8
				}
				_ = json.NewEncoder(w).Encode(api.AutomationResponse{Name: "flow", Version: version, Draft: definition})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "token")
			args := []string{"--app", "billing", "--name", "flow", "--expected-version", "7"}
			if mode != "no_scenarios" {
				args = append(args, "--scenarios", suite)
			}
			if mode != "advisory" {
				args = append(args, "--require-coverage")
			}
			code := cmdAutomationsPublish(args)
			passed := mode == "advisory" || mode == "excluded"
			if (code == 0) != passed {
				t.Fatalf("exit=%d calls=%v output=%s", code, calls, output)
			}
			if mode == "no_scenarios" || mode == "invalid_exclusion" {
				if len(calls) != 0 {
					t.Fatal("invalid options reached API")
				}
				return
			}
			wantCalls := 6
			if mode == "blocked" {
				wantCalls = 3
			}
			if len(calls) != wantCalls {
				t.Fatalf("calls=%v", calls)
			}
			var report automationCheckedPublishReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.PublicationAttempted != passed || report.Published != passed {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if mode == "blocked" && (!report.Checks.Scenarios[0].Passed || !strings.Contains(report.Error, "coverage gate")) {
				t.Fatal(report)
			}
		})
	}
}

func TestAutomationCoverageGateLoopIdentityAndSuggestions(t *testing.T) {
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "loop", ForEach: &api.WorkflowForEachSpec{Action: api.WorkflowForEachActionSpec{When: &api.WorkflowGuardSpec{}}}}, {Name: "loop/action", When: &api.WorkflowGuardSpec{}}}}
	exclusion := automationCoverageExclusion{Step: "loop/action", Loop: "loop", Code: "guard_skip_missing", Reason: "external item test"}
	if err := validateAutomationCoverageExclusions(definition, []automationCoverageExclusion{exclusion}); err != nil {
		t.Fatal(err)
	}
	report := automationCheckReport{Passed: true, CoverageHints: []automationCoverageHint{{Step: "loop/action", Loop: "loop", Code: "guard_skip_missing"}, {Step: "loop/action", Code: "guard_skip_missing"}}}
	applyAutomationCoverageGate(&report, true, []automationCoverageExclusion{exclusion})
	if report.Passed || report.Coverage.Remaining != 1 || len(unexcludedAutomationCoverageHints(report)) != 1 || unexcludedAutomationCoverageHints(report)[0].Loop != "" {
		t.Fatalf("exclusion crossed scope: %+v", report)
	}
	resetJSONOut(t)
	output := captureAutomationStdout(t)
	if code := printAutomationCheckReport(report); code != 1 || !strings.Contains(output.String(), "FAIL coverage gate") || !strings.Contains(output.String(), "external item test") {
		t.Fatalf("exit=%d output=%s", code, output)
	}
	report = automationCheckReport{Passed: false, CoverageHints: []automationCoverageHint{{Step: "loop/action", Loop: "loop", Code: "guard_skip_missing"}}}
	applyAutomationCoverageGate(&report, true, []automationCoverageExclusion{exclusion})
	if report.Passed || !report.Coverage.Passed || len(unexcludedAutomationCoverageHints(report)) != 0 {
		t.Fatal("exclusion masked an assertion failure")
	}
}

func TestAutomationCoverageGateFullyCovered(t *testing.T) {
	suite := coverageGateFixture(t, "")
	scenarios, err := loadAutomationScenarios(suite)
	if err != nil {
		t.Fatal(err)
	}
	second := scenarios[0]
	second.scenario = automationScenario{Name: "skipped", Expect: map[string]automationStepExpectation{"send": {State: "skipped"}}}
	second.request.Input = json.RawMessage(`{"active":false}`)
	scenarios = append(scenarios, second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request api.SimulateAutomationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		matched := string(request.Input) != `{"active":false}`
		status := "mocked"
		if !matched {
			status = "skipped"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SimulateAutomationResponse{DefinitionValid: true, Complete: true, Trace: []api.AutomationSimulationStep{{StepName: "send", State: status, WhenMatched: &matched}}})
	}))
	defer server.Close()
	report := runAutomationScenarios(context.Background(), api.NewClient(server.URL, "token"), "billing", scenarios, true)
	if !report.Passed || report.Coverage == nil || !report.Coverage.Passed || report.Coverage.Remaining != 0 || len(report.Coverage.Exclusions) != 0 {
		t.Fatalf("report=%+v", report)
	}
}
