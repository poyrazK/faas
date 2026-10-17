// Package automationlive checks explicit live assertions against terminal runs.
// It does not execute steps and never includes output values in its results.
package automationlive

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
)

func ExpectedRunStatus(test api.AutomationLiveTest) string {
	if test.ExpectedRunStatus == "" {
		return "succeeded"
	}
	return test.ExpectedRunStatus
}

func Validate(def api.WorkflowSpec, test api.AutomationLiveTest) error {
	raw, err := json.Marshal(test)
	if err != nil || int64(len(raw)) > api.AutomationLiveSpecMaxBytes || len(test.Expectations) == 0 || len(test.Expectations) > api.AutomationLiveMaxExpectations {
		return errors.New("live tests require a bounded, nonempty set of expectations")
	}
	switch ExpectedRunStatus(test) {
	case "succeeded", "failed", "dead":
	default:
		return errors.New("invalid expected live run status")
	}
	steps := map[string]api.WorkflowStepSpec{}
	for _, s := range def.Steps {
		steps[s.Name] = s
	}
	seen := map[string]bool{}
	for _, e := range test.Expectations {
		_, exists := steps[e.Step]
		key := e.Step
		if e.Loop != "" {
			loop, ok := steps[e.Loop]
			if !ok || loop.ForEach == nil || e.Step != e.Loop+"/action" || e.ItemIndex == nil || *e.ItemIndex < 0 || *e.ItemIndex >= api.WorkflowForEachMaxItems {
				return errors.New("invalid live loop assertion")
			}
			exists, key = true, e.Loop+"/"+strconv.Itoa(*e.ItemIndex)
		} else if e.ItemIndex != nil {
			return errors.New("live item index requires a loop")
		}
		if !exists || seen[key] || (e.State == "" && len(e.Output) == 0 && e.AttemptCount == nil) {
			return errors.New("unknown, duplicate, or empty live assertion")
		}
		seen[key] = true
		switch e.State {
		case "", "succeeded", "failed", "dead", "skipped":
		default:
			return errors.New("live expectations require real terminal step states")
		}
		if int64(len(e.Output)) > api.WorkflowRunInputMaxBytes || len(e.Output) > 0 && !json.Valid(e.Output) {
			return errors.New("invalid expected live output")
		}
		if e.AttemptCount != nil && (*e.AttemptCount < 0 || *e.AttemptCount > api.AutomationLiveMaxAttempts) {
			return errors.New("invalid expected live attempt count")
		}
	}
	return nil
}

type ObservedStep struct {
	State        string
	Output       json.RawMessage
	AttemptCount int
}

func Compare(e api.AutomationLiveExpectation, rows []ObservedStep) api.AutomationTestStepResult {
	result := api.AutomationTestStepResult{Step: e.Step, Loop: e.Loop, ItemIndex: e.ItemIndex, Passed: true}
	if len(rows) != 1 {
		result.Passed, result.Mismatches = false, []string{"step"}
		return result
	}
	r := rows[0]
	result.State = r.State
	if e.State != "" && e.State != r.State {
		result.Mismatches = append(result.Mismatches, "state")
	}
	if len(e.Output) > 0 && !automationchecks.EqualJSON(e.Output, r.Output) {
		result.Mismatches = append(result.Mismatches, "output")
	}
	if e.AttemptCount != nil && *e.AttemptCount != r.AttemptCount {
		result.Mismatches = append(result.Mismatches, "attempt_count")
	}
	result.Passed = len(result.Mismatches) == 0
	return result
}
