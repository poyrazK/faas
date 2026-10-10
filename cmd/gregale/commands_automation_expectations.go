package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationItemExpectationKey struct {
	loop  string
	index int
}

type automationAttemptExpectation struct {
	Outcome    string `yaml:"outcome"`
	HTTPStatus *int   `yaml:"http_status"`
}

func automationExpectationItemIndex(value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index >= api.WorkflowForEachMaxItems || strconv.Itoa(index) != value {
		return 0, errors.New("item expectation indexes must be canonical zero-based indexes below 128")
	}
	return index, nil
}

func prepareAutomationExpectation(expect automationStepExpectation, label string) (json.RawMessage, error) {
	if expect.State != "" && !validAutomationScenarioState(expect.State) {
		return nil, fmt.Errorf("%s has an invalid expected state", label)
	}
	if expect.State == "" && expect.Reason == nil && expect.WhenMatched == nil && expect.Output.Kind == 0 && expect.AttemptCount == nil && expect.Attempts == nil {
		return nil, fmt.Errorf("%s has an empty expectation", label)
	}
	if expect.AttemptCount != nil && (*expect.AttemptCount < 0 || *expect.AttemptCount > api.WorkflowRetryMaxAttempts) {
		return nil, fmt.Errorf("%s attempt_count must be 0..25", label)
	}
	if expect.Attempts != nil {
		if len(*expect.Attempts) > api.WorkflowRetryMaxAttempts {
			return nil, fmt.Errorf("%s attempts must have at most 25 entries", label)
		}
		if expect.AttemptCount != nil && *expect.AttemptCount != len(*expect.Attempts) {
			return nil, fmt.Errorf("%s attempt_count differs from the expected attempts length", label)
		}
		for _, attempt := range *expect.Attempts {
			if attempt.Outcome != "success" && attempt.Outcome != "failure" && attempt.Outcome != "timeout" {
				return nil, fmt.Errorf("%s attempt outcome must be success, failure or timeout", label)
			}
			if attempt.HTTPStatus != nil && (attempt.Outcome != "failure" || *attempt.HTTPStatus < 100 || *attempt.HTTPStatus > 599 || (*attempt.HTTPStatus >= 200 && *attempt.HTTPStatus < 300)) {
				return nil, fmt.Errorf("%s expected http_status requires a failure and non-2xx status from 100 to 599", label)
			}
		}
	}
	if expect.Output.Kind == 0 {
		return nil, nil
	}
	value, err := automationScenarioExpectedValue(&expect.Output, 0)
	if err != nil {
		return nil, fmt.Errorf("%s expected output: %w", label, err)
	}
	output, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s expected output must be JSON-compatible", label)
	}
	if int64(len(output)) > api.WorkflowRunInputMaxBytes {
		return nil, errors.New("expected output exceeds the workflow value byte limit")
	}
	return output, nil
}

func evaluateAutomationStepExpectation(label string, expect automationStepExpectation, output json.RawMessage, hasOutput bool, row api.AutomationSimulationStep) []string {
	failures := []string{}
	if expect.State != "" && row.State != expect.State {
		failures = append(failures, fmt.Sprintf("%s state: expected %s, got %s", label, expect.State, row.State))
	}
	if expect.Reason != nil && row.Reason != *expect.Reason {
		failures = append(failures, label+" reason does not match")
	}
	if expect.WhenMatched != nil && (row.WhenMatched == nil || *row.WhenMatched != *expect.WhenMatched) {
		failures = append(failures, label+" when_matched does not match")
	}
	if hasOutput && !equalAutomationScenarioJSON(output, row.Output) {
		failures = append(failures, label+" output does not match")
	}
	if expect.AttemptCount != nil && len(row.Attempts) != *expect.AttemptCount {
		failures = append(failures, fmt.Sprintf("%s attempt_count: expected %d, got %d", label, *expect.AttemptCount, len(row.Attempts)))
	}
	if expect.Attempts != nil {
		matched := len(*expect.Attempts) == len(row.Attempts)
		if matched {
			for index, expected := range *expect.Attempts {
				actual := row.Attempts[index]
				if actual.Attempt != index+1 || actual.Outcome != expected.Outcome || (expected.HTTPStatus != nil && (actual.HTTPStatus == nil || *actual.HTTPStatus != *expected.HTTPStatus)) {
					matched = false
					break
				}
			}
		}
		if !matched {
			failures = append(failures, label+" attempts do not match")
		}
	}
	return failures
}
