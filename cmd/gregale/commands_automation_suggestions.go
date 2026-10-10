package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

type automationSuggestionSummary struct {
	Path      string `json:"path,omitempty"`
	Count     int    `json:"count"`
	Remaining int    `json:"remaining"`
}

// Suggestions intentionally never reuse existing samples or resolved trace values.
func writeAutomationSuggestions(path, definitionPath string, definition api.WorkflowSpec, hints []automationCoverageHint) (automationSuggestionSummary, error) {
	summary := automationSuggestionSummary{}
	if len(hints) == 0 {
		return summary, nil
	}
	definitionPath, err := filepath.Abs(definitionPath)
	if err != nil {
		return summary, err
	}
	steps := map[string]api.WorkflowStepSpec{}
	for _, step := range definition.Steps {
		steps[step.Name] = step
	}
	entries := []any{}
	comments := []string{}
	for _, hint := range hints {
		if len(entries) == 32 {
			break
		}
		step, nested := steps[hint.Step], false
		if hint.Loop != "" {
			step = steps[hint.Loop]
			nested = step.ForEach != nil
		}
		if step.Name == "" {
			continue
		}
		number := len(entries) + 1
		stem := fmt.Sprintf("TODO-suggestion-%02d", number)
		scenario := map[string]any{"name": fmt.Sprintf("suggestion-%02d-%s", number, hint.Code), "input_file": stem + "-input.json"}
		expect := map[string]any{}
		comment := "Coverage: " + hint.Step + " [" + hint.Code + "].\nTODO: " + hint.Message + "\nCreate " + stem + "-input.json using synthetic input that reaches this step.\nMock prerequisites and remaining steps as needed so the trace is complete."
		mockField := "mock_outputs_file"
		mocks := map[string]any{step.Name: map[string]any{"TODO_REPLACE": true}}
		switch hint.Code {
		case "guard_match_missing", "guard_skip_missing":
			matched := hint.Code == "guard_match_missing"
			expect["when_matched"] = matched
			if !matched {
				expect["state"] = "skipped"
				mocks = nil
			} else if nested {
				expect["state"] = "mocked"
			} else if step.ForEach != nil {
				expect["state"] = "resolved"
			} else if step.WaitForEvent != "" || step.WaitForCallback || step.WaitForCondition != nil || step.WaitForDuration > 0 {
				expect["state"] = "would_wait"
				mocks = nil
			} else {
				expect["state"] = "mocked"
			}
			comment += "\nTODO: adjust input/dependency outputs so the guard evaluates to the expected boolean."
			if nested && mocks != nil {
				mockField = "mock_item_attempts_file"
				mocks = map[string]any{step.Name: map[string]any{"0": []any{map[string]any{"outcome": "success", "output": map[string]any{"TODO_REPLACE": true}}}}}
			}
			if !nested && step.ForEach != nil && mocks != nil {
				mockField = "mock_item_outputs_file"
				mocks = map[string]any{step.Name: []any{map[string]any{"TODO_REPLACE": true}}}
			}
			if expect["state"] == "would_wait" {
				comment += "\nTODO: supply a supported wait outcome and change this state expectation before running; unresolved waits cannot pass a complete check."
			}
		case "failure_route_missing":
			expect["state"] = "failed"
			expect["attempt_count"] = 1
			mockField = "mock_attempts_file"
			mocks = map[string]any{step.Name: []any{map[string]any{"outcome": "failure", "http_status": 400}}}
			scenario["expect"] = map[string]any{step.Name: expect, step.OnFailure: map[string]any{"state": "mocked"}}
			handlerPath := stem + "-handler-mocks.json"
			scenario["mock_outputs_file"] = handlerPath
			handlerSample, _ := json.MarshalIndent(map[string]any{step.OnFailure: map[string]any{"TODO_REPLACE": true}}, "", "  ")
			comment += "\nCreate " + handlerPath + " with this handler output starter JSON:\n" + string(handlerSample) + "\nTODO: verify any guarded prerequisites."
		case "wait_success_missing":
			expect["state"] = "mocked"
			expect["attempts"] = []any{map[string]any{"outcome": "success"}}
			mockField = "mock_attempts_file"
			mocks = map[string]any{step.Name: []any{map[string]any{"outcome": "success", "output": map[string]any{"TODO_REPLACE": true}}}}
		case "wait_timeout_missing":
			expect["state"] = "timed_out"
			expect["attempts"] = []any{map[string]any{"outcome": "timeout"}}
			mockField = "mock_attempts_file"
			mocks = map[string]any{step.Name: []any{map[string]any{"outcome": "timeout"}}}
			comment += "\nTODO: add outputs for the timeout handler and other reached actions."
		case "retry_missing":
			expect["state"] = "mocked"
			expect["attempt_count"] = 2
			expect["attempts"] = []any{map[string]any{"outcome": "failure", "http_status": 503}, map[string]any{"outcome": "success"}}
			attempts := []any{map[string]any{"outcome": "failure", "http_status": 503}, map[string]any{"outcome": "success", "output": map[string]any{"TODO_REPLACE": true}}}
			mockField = "mock_attempts_file"
			mocks = map[string]any{step.Name: attempts}
			if nested {
				mockField = "mock_item_attempts_file"
				mocks = map[string]any{step.Name: map[string]any{"0": attempts}}
			}
			comment += "\nTODO: verify the action is safe to retry, reach it, and replace its successful output."
		case "loop_empty_missing":
			expect["state"] = "resolved"
			expect["output"] = []any{}
			mocks = nil
			comment += "\nTODO: make the loop's items expression resolve to an empty array; for dependency-backed collections mock the source step."
		case "loop_multiple_items_missing":
			expect["state"] = "resolved"
			mockField = "mock_item_outputs_file"
			mocks = map[string]any{step.Name: []any{map[string]any{"TODO_REPLACE": true}, map[string]any{"TODO_REPLACE": true}}}
			comment += "\nTODO: make the items expression resolve to at least two synthetic items and assert their collected outputs."
		default:
			continue
		}
		if nested {
			scenario["expect_items"] = map[string]any{step.Name: map[string]any{"0": expect}}
		} else if _, exists := scenario["expect"]; !exists {
			scenario["expect"] = map[string]any{step.Name: expect}
		}
		if mocks != nil {
			mockPath := stem + "-mocks.json"
			scenario[mockField] = mockPath
			sample, err := json.MarshalIndent(mocks, "", "  ")
			if err != nil {
				return summary, err
			}
			comment += "\nCreate " + mockPath + " with this starter JSON (replace TODO values):\n" + string(sample)
		}
		entries = append(entries, scenario)
		comments = append(comments, comment)
	}
	if len(entries) == 0 {
		return summary, errors.New("coverage hints could not be mapped to definition steps")
	}
	var node yaml.Node
	if err := node.Encode(map[string]any{"definition": definitionPath, "scenarios": entries}); err != nil {
		return summary, err
	}
	node.HeadComment = "REVIEW REQUIRED: these are incomplete starter scenarios, not verified checks.\nCreate the referenced TODO input/mock files, adapt prerequisites and expectations, then run check.\nNo existing sample inputs, outputs or error values were copied."
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "scenarios" {
			for index, entry := range node.Content[i+1].Content {
				entry.HeadComment = comments[index]
			}
		}
	}
	data, err := yaml.Marshal(&node)
	if err != nil {
		return summary, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return summary, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return summary, errors.Join(writeErr, closeErr)
	}
	summary.Path, summary.Count, summary.Remaining = path, len(entries), len(hints)-len(entries)
	return summary, nil
}
