// Package automationchecks evaluates assertions and coverage on simulated traces.
// It never executes an automation or retains sample data.
package automationchecks

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateExpectations(def api.WorkflowSpec, expectations []api.AutomationCheckExpectation) error {
	if len(expectations) == 0 || len(expectations) > api.AutomationPublishCheckMaxExpectations {
		return errors.New("each scenario requires 1..4096 expectations")
	}
	steps := map[string]api.WorkflowStepSpec{}
	for _, s := range def.Steps {
		steps[s.Name] = s
	}
	type expectationKey struct {
		step, loop string
		index      int
	}
	seen := map[expectationKey]bool{}
	for _, e := range expectations {
		key := expectationKey{step: e.Step}
		step, ok := steps[e.Step]
		if e.Loop != "" {
			step, ok = steps[e.Loop]
			if !ok || step.ForEach == nil || e.Step != e.Loop+"/action" || e.ItemIndex == nil || *e.ItemIndex < 0 || *e.ItemIndex >= api.WorkflowForEachMaxItems {
				return errors.New("invalid item expectation")
			}
			key = expectationKey{loop: e.Loop, index: *e.ItemIndex}
		} else if e.ItemIndex != nil {
			return errors.New("item_index requires loop")
		}
		if !ok || seen[key] {
			return errors.New("unknown or duplicate expectation")
		}
		seen[key] = true
		if e.State == "" && e.Reason == nil && e.WhenMatched == nil && len(e.Output) == 0 && e.AttemptCount == nil && e.Attempts == nil {
			return errors.New("empty expectation")
		}
		if e.State != "" {
			switch e.State {
			case "mocked", "would_execute", "resolved", "expanded", "would_wait", "would_retry", "timed_out", "failed", "dead", "skipped", "blocked", "error":
			default:
				return errors.New("invalid expected state")
			}
		}
		if len(e.Output) > int(api.WorkflowRunInputMaxBytes) || (len(e.Output) > 0 && !json.Valid(e.Output)) {
			return errors.New("invalid expected output")
		}
		if e.AttemptCount != nil && (*e.AttemptCount < 0 || *e.AttemptCount > api.WorkflowRetryMaxAttempts) {
			return errors.New("invalid attempt count")
		}
		if e.Attempts != nil {
			if len(*e.Attempts) > api.WorkflowRetryMaxAttempts || (e.AttemptCount != nil && *e.AttemptCount != len(*e.Attempts)) {
				return errors.New("invalid attempts")
			}
			for _, a := range *e.Attempts {
				if a.Outcome != "success" && a.Outcome != "failure" && a.Outcome != "timeout" {
					return errors.New("invalid attempt outcome")
				}
				if a.HTTPStatus != nil && (a.Outcome != "failure" || *a.HTTPStatus < 100 || *a.HTTPStatus > 599 || (*a.HTTPStatus >= 200 && *a.HTTPStatus < 300)) {
					return errors.New("invalid HTTP status expectation")
				}
			}
		}
	}
	return nil
}

func Passes(expectations []api.AutomationCheckExpectation, response api.SimulateAutomationResponse) bool {
	if !response.DefinitionValid || !response.Complete || len(response.Issues) > 0 {
		return false
	}
	for _, e := range expectations {
		var row api.AutomationSimulationStep
		matches := 0
		for _, r := range response.Trace {
			if e.Loop == "" {
				if r.ParentStep != "" || r.ItemIndex != nil || r.StepName != e.Step {
					continue
				}
			} else if r.ParentStep != e.Loop || r.ItemIndex == nil || *r.ItemIndex != *e.ItemIndex {
				continue
			}
			row = r
			matches++
		}
		if matches != 1 || (e.State != "" && row.State != e.State) || (e.Reason != nil && row.Reason != *e.Reason) || (e.WhenMatched != nil && (row.WhenMatched == nil || *row.WhenMatched != *e.WhenMatched)) || (len(e.Output) > 0 && !EqualJSON(e.Output, row.Output)) || (e.AttemptCount != nil && len(row.Attempts) != *e.AttemptCount) {
			return false
		}
		if e.Attempts != nil {
			if len(*e.Attempts) != len(row.Attempts) {
				return false
			}
			for i, a := range *e.Attempts {
				r := row.Attempts[i]
				if r.Attempt != i+1 || a.Outcome != r.Outcome || (a.HTTPStatus != nil && (r.HTTPStatus == nil || *a.HTTPStatus != *r.HTTPStatus)) {
					return false
				}
			}
		}
	}
	return true
}

func RemainingCoverage(def api.WorkflowSpec, coverage Coverage, exclusions []api.AutomationCheckExclusion) (int, error) {
	if len(exclusions) > api.AutomationPublishCheckMaxExclusions {
		return 0, errors.New("too many exclusions")
	}
	type key struct{ step, loop, code string }
	available := map[key]bool{}
	excluded := map[key]bool{}
	for _, h := range (Coverage{}).Hints(def) {
		available[key{h.Step, h.Loop, h.Code}] = true
	}
	for _, x := range exclusions {
		k := key{x.Step, x.Loop, x.Code}
		if !available[k] || excluded[k] || strings.TrimSpace(x.Reason) == "" || len(x.Reason) > 512 || strings.IndexFunc(x.Reason, unicode.IsControl) >= 0 {
			return 0, errors.New("invalid coverage exclusion")
		}
		excluded[k] = true
	}
	remaining := 0
	for _, h := range coverage.Hints(def) {
		if !excluded[key{h.Step, h.Loop, h.Code}] {
			remaining++
		}
	}
	return remaining, nil
}
