package automationchecks

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Coverage is observational: a passing scenario must actually exercise an outcome.
// Expectations or unused mocks alone do not establish coverage.
type Hint struct {
	Loop    string `json:"loop,omitempty"`
	Step    string `json:"step"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Coverage struct {
	traces []automationCoverageTrace
}

type automationCoverageTrace struct {
	roots map[string]api.AutomationSimulationStep
	items map[string][]api.AutomationSimulationStep
}

func (coverage *Coverage) Observe(response api.SimulateAutomationResponse) {
	trace := automationCoverageTrace{roots: map[string]api.AutomationSimulationStep{}, items: map[string][]api.AutomationSimulationStep{}}
	for _, row := range response.Trace {
		// Retain only control outcomes, never sample inputs, outputs or target values.
		outcome := api.AutomationSimulationStep{State: row.State, Reason: row.Reason, WhenMatched: row.WhenMatched, Attempts: row.Attempts, ItemCount: row.ItemCount, ItemIndex: row.ItemIndex}
		if row.ParentStep == "" && row.ItemIndex == nil {
			trace.roots[row.StepName] = outcome
		} else if row.ParentStep != "" && row.ItemIndex != nil && *row.ItemIndex >= 0 {
			trace.items[row.ParentStep] = append(trace.items[row.ParentStep], outcome)
		}
	}
	coverage.traces = append(coverage.traces, trace)
}

func (coverage Coverage) Hints(definition api.WorkflowSpec) []Hint {
	hints := []Hint{}
	steps := append([]api.WorkflowStepSpec(nil), definition.Steps...)
	sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })
	reached := func(row api.AutomationSimulationStep) bool {
		switch row.State {
		case "mocked", "would_execute", "resolved", "expanded", "would_wait", "would_retry", "timed_out", "failed", "dead":
			return true
		}
		return false
	}
	for _, step := range steps {
		matched, skipped, failureRoute, waitSuccess, waitTimeout, retried := false, false, false, false, false, false
		for _, trace := range coverage.traces {
			rows := trace.roots
			row, exists := rows[step.Name]
			if !exists {
				continue
			}
			if row.WhenMatched != nil {
				matched = matched || *row.WhenMatched
				skipped = skipped || (!*row.WhenMatched && row.State == "skipped")
			}
			if row.State == "failed" || row.State == "dead" {
				handler, exists := rows[step.OnFailure]
				failureRoute = failureRoute || (exists && reached(handler))
			}
			waitSuccess = waitSuccess || (row.State == "mocked" && (row.Reason == "event_received_mocked" || row.Reason == "callback_received_mocked"))
			waitTimeout = waitTimeout || (row.State == "timed_out" && row.Reason == "timeout_mocked")
			for _, attempt := range row.Attempts {
				retried = retried || attempt.Attempt > 1
			}
		}
		add := func(code, message string) {
			hints = append(hints, Hint{Step: step.Name, Code: code, Message: message})
		}
		if step.When != nil {
			if !matched {
				add("guard_match_missing", "Add a passing scenario where this guard matches.")
			}
			if !skipped {
				add("guard_skip_missing", "Add a passing scenario where this guard skips the step.")
			}
		}
		if step.OnFailure != "" && !failureRoute {
			add("failure_route_missing", "Add a passing scenario where this step fails and its failure handler is reached.")
		}
		eventWait := step.WaitForEvent != "" || step.WaitForCallback
		if eventWait && !waitSuccess {
			add("wait_success_missing", "Add a passing scenario with a received event or callback payload.")
		}
		if (eventWait || step.WaitForCondition != nil) && step.Timeout > 0 && step.OnTimeout != "" && !waitTimeout {
			add("wait_timeout_missing", "Add a passing scenario with a mocked wait timeout.")
		}
		if step.Retry != nil && step.Retry.MaxAttempts > 1 && !retried {
			add("retry_missing", "Add a passing scenario that exercises a second attempt.")
		}
		if step.ForEach != nil {
			hints = append(hints, coverage.loopHints(step)...)
		}
	}
	return hints
}

// Loop actions cannot contain nested loops, waits or failure routes in the current schema.
func (coverage Coverage) loopHints(step api.WorkflowStepSpec) []Hint {
	hints := []Hint{}
	empty, multiple, matched, skipped, retried := false, false, false, false, false
	for _, trace := range coverage.traces {
		parent, exists := trace.roots[step.Name]
		if !exists || parent.ItemCount == nil || (parent.State != "expanded" && parent.State != "resolved" && parent.State != "failed" && parent.State != "dead") {
			continue
		}
		empty = empty || (*parent.ItemCount == 0 && parent.State == "resolved")
		multiple = multiple || *parent.ItemCount >= 2
		// Empty loops have no action outcomes; unrelated or orphan item rows do not count.
		if *parent.ItemCount <= 0 {
			continue
		}
		for _, row := range trace.items[step.Name] {
			if *row.ItemIndex >= *parent.ItemCount {
				continue
			}
			if row.WhenMatched != nil {
				matched = matched || *row.WhenMatched
				skipped = skipped || (!*row.WhenMatched && row.State == "skipped")
			}
			for _, attempt := range row.Attempts {
				retried = retried || attempt.Attempt > 1
			}
		}
	}
	add := func(path, code, message string) {
		hint := Hint{Step: path, Code: code, Message: message}
		if path == step.Name+"/action" {
			hint.Loop = step.Name
		}
		hints = append(hints, hint)
	}
	if !empty {
		add(step.Name, "loop_empty_missing", "Add a passing scenario with an empty collection for this loop.")
	}
	if !multiple {
		add(step.Name, "loop_multiple_items_missing", "Add a passing scenario with at least two items for this loop.")
	}
	action := step.ForEach.Action
	if action.When != nil {
		if !matched {
			add(step.Name+"/action", "guard_match_missing", "Add a passing scenario where an item action guard matches.")
		}
		if !skipped {
			add(step.Name+"/action", "guard_skip_missing", "Add a passing scenario where an item action guard skips the item.")
		}
	}
	if action.Retry != nil && action.Retry.MaxAttempts > 1 && !retried {
		add(step.Name+"/action", "retry_missing", "Add a passing scenario with item attempt mocks that exercise a second attempt.")
	}
	return hints
}
