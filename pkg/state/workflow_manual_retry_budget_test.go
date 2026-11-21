package state

import (
	"encoding/json"
	"testing"
)

func TestRegressionManualRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		resume, attempt, base, limit int
	}{
		{"initial_generation_one_attempt", 0, 1, 0, 1},
		{"initial_generation_three_attempts", 0, 3, 0, 3},
		{"resumed_generation_control", 1, 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition, _ := json.Marshal(map[string]any{"name": "root-cause", "steps": []any{map[string]any{"name": "work", "path": "/work", "retry": map[string]any{"max_attempts": tc.limit}}}})
			run := &WorkflowRun{ResumeCount: tc.resume, DefinitionSnapshot: definition}
			base := workflowManualRetryBase(run, "work", tc.attempt, tc.base)
			remaining := tc.limit - (tc.attempt - base)
			if remaining != 1 {
				t.Errorf("accepted manual retry leaves %d available dispatches: resume_count=%d attempt=%d retry_base=%d limit=%d", remaining, tc.resume, tc.attempt, base, tc.limit)
			}
		})
	}
}
