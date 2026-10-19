package automationchecks

import "github.com/onebox-faas/faas/pkg/api"

// MismatchFields identifies failed assertion fields without retaining expected or
// observed values. A missing or ambiguous trace target fails closed.
func MismatchFields(e api.AutomationCheckExpectation, trace []api.AutomationSimulationStep) []string {
	var row api.AutomationSimulationStep
	matches := 0
	for _, r := range trace {
		if e.Loop == "" {
			if r.ParentStep != "" || r.ItemIndex != nil || r.StepName != e.Step {
				continue
			}
		} else if e.ItemIndex == nil || r.ParentStep != e.Loop || r.ItemIndex == nil || *r.ItemIndex != *e.ItemIndex {
			continue
		}
		row = r
		matches++
	}
	if matches != 1 {
		return []string{"step"}
	}
	var fields []string
	if e.State != "" && row.State != e.State {
		fields = append(fields, "state")
	}
	if e.Reason != nil && row.Reason != *e.Reason {
		fields = append(fields, "reason")
	}
	if e.WhenMatched != nil && (row.WhenMatched == nil || *row.WhenMatched != *e.WhenMatched) {
		fields = append(fields, "when_matched")
	}
	if len(e.Output) > 0 && !EqualJSON(e.Output, row.Output) {
		fields = append(fields, "output")
	}
	if e.AttemptCount != nil && len(row.Attempts) != *e.AttemptCount {
		fields = append(fields, "attempt_count")
	}
	if e.Attempts != nil && !attemptsMatch(*e.Attempts, row.Attempts) {
		fields = append(fields, "attempts")
	}
	return fields
}

func attemptsMatch(expected []api.AutomationCheckAttempt, actual []api.AutomationSimulationAttempt) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i, e := range expected {
		r := actual[i]
		if r.Attempt != i+1 || e.Outcome != r.Outcome || (e.HTTPStatus != nil && (r.HTTPStatus == nil || *e.HTTPStatus != *r.HTTPStatus)) {
			return false
		}
	}
	return true
}
