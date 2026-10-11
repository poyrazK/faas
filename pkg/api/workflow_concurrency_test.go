package api

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestWorkflowMaxConcurrentRunsValidation(t *testing.T) {
	base := WorkflowSpec{Name: "bounded", Steps: []WorkflowStepSpec{{Name: "run", Run: "handler"}}}
	for _, test := range []struct {
		name  string
		limit int
		valid bool
	}{
		{name: "unspecified", valid: true},
		{name: "one", limit: 1, valid: true},
		{name: "maximum", limit: WorkflowMaxConcurrentRunsLimit, valid: true},
		{name: "negative", limit: -1},
		{name: "above maximum", limit: WorkflowMaxConcurrentRunsLimit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			spec.MaxConcurrentRuns = test.limit
			_, err := ValidateWorkflowDAG(spec, PlanPro)
			if test.valid && err != nil {
				t.Fatalf("ValidateWorkflowDAG() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrWorkflowConcurrencyInvalid) {
				t.Fatalf("ValidateWorkflowDAG() error = %v, want %v", err, ErrWorkflowConcurrencyInvalid)
			}
		})
	}
}

func TestWorkflowMaxConcurrentActionsValidation(t *testing.T) {
	base := WorkflowSpec{Name: "bounded-actions", Steps: []WorkflowStepSpec{{Name: "send", Run: "handler"}}}
	for _, test := range []struct {
		name  string
		limit int
		valid bool
	}{
		{name: "unspecified", valid: true},
		{name: "one", limit: 1, valid: true},
		{name: "maximum", limit: WorkflowMaxConcurrentActionsLimit, valid: true},
		{name: "negative", limit: -1},
		{name: "above maximum", limit: WorkflowMaxConcurrentActionsLimit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			spec.MaxConcurrentActions = test.limit
			_, err := ValidateWorkflowDAG(spec, PlanPro)
			if test.valid && err != nil {
				t.Fatalf("ValidateWorkflowDAG() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrWorkflowActionConcurrencyInvalid) {
				t.Fatalf("ValidateWorkflowDAG() error = %v, want %v", err, ErrWorkflowActionConcurrencyInvalid)
			}
		})
	}

	spec := WorkflowSpec{Name: "round-trip", MaxConcurrentActions: 7, Steps: []WorkflowStepSpec{{Name: "send", Run: "handler"}}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkflowSpec
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.MaxConcurrentActions != spec.MaxConcurrentActions {
		t.Fatalf("max_concurrent_actions round trip: got %d err=%v", decoded.MaxConcurrentActions, err)
	}
}
