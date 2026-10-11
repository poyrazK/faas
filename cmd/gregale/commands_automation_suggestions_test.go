package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"gopkg.in/yaml.v3"
)

func TestAutomationSuggestionTemplates(t *testing.T) {
	definition := api.WorkflowSpec{Name: "sample", Steps: []api.WorkflowStepSpec{
		{Name: "guard", Run: "send", When: &api.WorkflowGuardSpec{Ref: "input.enabled", Op: "eq", Value: json.RawMessage(`true`)}},
		{Name: "action", Run: "send", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}, OnFailure: "recover"},
		{Name: "recover", Run: "recover"},
		{Name: "wait", WaitForCallback: true, Timeout: time.Hour, OnTimeout: "expire"},
		{Name: "expire", Run: "expire"},
		{Name: "loop", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "send", When: &api.WorkflowGuardSpec{Ref: "input.item.active", Op: "eq", Value: json.RawMessage(`true`)}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}}}},
	}}
	hints := automationScenarioCoverage{}.hints(definition)
	path := filepath.Join(t.TempDir(), "suggestions.yaml")
	summary, err := writeAutomationSuggestions(path, "automation.yaml", definition, hints)
	if err != nil || summary.Count != len(hints) || summary.Remaining != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "REVIEW REQUIRED") || !strings.Contains(string(raw), `"http_status": 503`) || !strings.Contains(string(raw), "TODO_REPLACE") {
		t.Fatalf("missing review guidance: %s", raw)
	}
	var suite automationScenarioSuite
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&suite); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(suite.Definition) || len(suite.Scenarios) != len(hints) {
		t.Fatal(suite)
	}
	for i, scenario := range suite.Scenarios {
		if !strings.HasPrefix(scenario.InputFile, "TODO-") {
			t.Fatal("missing input placeholder")
		}
		hint := hints[i]
		if hint.Loop != "" {
			if len(scenario.ExpectItems[hint.Loop]) != 1 || len(scenario.Expect) != 0 {
				t.Fatalf("item suggestion lost loop identity: %+v", scenario)
			}
			if hint.Code == "retry_missing" && scenario.MockItemAttemptsFile == "" {
				t.Fatal("missing item attempt mock reference")
			}
		} else if len(scenario.Expect) == 0 {
			t.Fatal("missing root expectation")
		}
		for name, expect := range scenario.Expect {
			if _, err := prepareAutomationExpectation(expect, name); err != nil {
				t.Fatal(err)
			}
		}
		for loop, indexes := range scenario.ExpectItems {
			for _, expect := range indexes {
				if _, err := prepareAutomationExpectation(expect, loop); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
}

func TestAutomationSuggestionsReviewThenRun(t *testing.T) {
	dir := t.TempDir()
	definitionPath := filepath.Join(dir, "automation.yaml")
	if err := os.WriteFile(definitionPath, []byte("name: sample\nsteps:\n  - name: guard\n    run: send\n    when: {ref: input.enabled, op: eq, value: true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	definition, ok := readAutomationDefinition(definitionPath)
	if !ok {
		t.Fatal("definition")
	}
	suggestion := filepath.Join(dir, "suggestions.yaml")
	if _, err := writeAutomationSuggestions(suggestion, definitionPath, definition, []automationCoverageHint{{Step: "guard", Code: "guard_skip_missing"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAutomationScenarios(suggestion); err == nil {
		t.Fatal("unreviewed TODO files were runnable")
	}
	// Complete the one required input file using synthetic input, then run the generated suite.
	if err := os.WriteFile(filepath.Join(dir, "TODO-suggestion-01-input.json"), []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAutomationScenarios(suggestion)
	if err != nil {
		t.Fatal(err)
	}
	response, err := state.SimulateAutomation(context.Background(), loaded[0].request, api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if result := evaluateAutomationScenario(loaded[0], response); !result.Passed {
		t.Fatal(result)
	}
}

func TestAutomationSuggestionsNoOverwriteAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suggestions.yaml")
	definition := api.WorkflowSpec{Steps: []api.WorkflowStepSpec{{Name: "guard", Run: "send"}}}
	hints := []automationCoverageHint{{Step: "guard", Code: "guard_match_missing"}}
	if err := os.WriteFile(path, []byte("original-private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAutomationSuggestions(path, "definition.yaml", definition, hints); !os.IsExist(err) {
		t.Fatalf("overwrite error=%v", err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAutomationSuggestions(link, "definition.yaml", definition, hints); !os.IsExist(err) {
		t.Fatalf("symlink error=%v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "original-private-value" {
		t.Fatal("overwrote destination")
	}
	empty := filepath.Join(dir, "empty.yaml")
	if summary, err := writeAutomationSuggestions(empty, "definition.yaml", definition, nil); err != nil || summary.Count != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if _, err := os.Lstat(empty); !os.IsNotExist(err) {
		t.Fatal("created empty suggestion file")
	}
	many := make([]automationCoverageHint, 34)
	for i := range many {
		many[i] = hints[0]
	}
	capped := filepath.Join(dir, "capped.yaml")
	if summary, err := writeAutomationSuggestions(capped, "definition.yaml", definition, many); err != nil || summary.Count != 32 || summary.Remaining != 2 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestCmdAutomationSuggestions(t *testing.T) {
	for _, mode := range []string{"human", "json", "existing", "out_without_suggest", "no_hints", "failed_check"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode == "json" || mode == "failed_check"
			output := captureAutomationStdout(t)
			dir := t.TempDir()
			definition := filepath.Join(dir, "definition.yaml")
			spec := "name: sample\nsteps:\n  - name: guard\n    run: send\n"
			if mode != "no_hints" {
				spec += "    when: {ref: input.enabled, op: eq, value: true}\n"
			}
			if err := os.WriteFile(definition, []byte(spec), 0600); err != nil {
				t.Fatal(err)
			}
			suite := filepath.Join(dir, "suite.yaml")
			if err := os.WriteFile(suite, []byte("definition: definition.yaml\nscenarios:\n  - name: initial\n    expect:\n      guard: {state: skipped}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			body := `{"definition_valid":true,"complete":true,"issues":[],"trace":[{"step_name":"guard","state":"skipped","when_matched":false,"output":{"secret":"private-value"}}]}`
			if mode == "failed_check" {
				body = `{"definition_valid":true,"complete":true,"issues":[],"trace":[{"step_name":"guard","state":"mocked","when_matched":true}]}`
			}
			fake := authedFakeAPI(t, body, 200)
			target := filepath.Join(dir, "scenario-suggestions.yaml")
			args := []string{"--app", "billing", "--scenarios", suite, "--suggest"}
			if mode == "existing" {
				if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "out_without_suggest" {
				args = []string{"--app", "billing", "--scenarios", suite, "--suggest-out", target}
			}
			code := cmdAutomationsCheck(args)
			if mode == "existing" || mode == "out_without_suggest" {
				if code == 0 || fake.sawMethod != "" {
					t.Fatalf("exit=%d request=%s", code, fake.sawMethod)
				}
				return
			}
			if (code == 0) != (mode != "failed_check") {
				t.Fatalf("exit=%d output=%s", code, output)
			}
			if mode == "no_hints" {
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatal("created file without hints")
				}
				return
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private-value") || strings.Contains(output.String(), "private-value") {
				t.Fatal("copied private sample value")
			}
			if jsonOutput {
				var report automationCheckReport
				if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Suggestions == nil || report.Suggestions.Path != target || report.Suggestions.Count == 0 {
					t.Fatalf("report=%+v err=%v", report, err)
				}
			} else if !strings.Contains(output.String(), "scenario starters") {
				t.Fatal("missing summary")
			}
		})
	}
}
