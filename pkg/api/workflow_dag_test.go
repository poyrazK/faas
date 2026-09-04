package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateWorkflowDAG(t *testing.T) {
	tests := []struct {
		name      string
		spec      WorkflowSpec
		plan      Plan
		wantErr   error
		wantOrder int // expected sorted length
	}{
		{
			name: "linear chain A -> B -> C",
			spec: WorkflowSpec{
				Name: "linear",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", Method: "POST"},
					{Name: "step_b", Path: "/b", DependsOn: []string{"step_a"}},
					{Name: "step_c", Path: "/c", DependsOn: []string{"step_b"}},
				},
			},
			plan:      PlanHobby,
			wantOrder: 3,
		},
		{
			name: "fan-out A -> {B, C} -> D",
			spec: WorkflowSpec{
				Name: "diamond",
				Steps: []WorkflowStepSpec{
					{Name: "start", Path: "/start"},
					{Name: "worker_1", Path: "/w1", DependsOn: []string{"start"}},
					{Name: "worker_2", Path: "/w2", DependsOn: []string{"start"}},
					{Name: "aggregate", Path: "/agg", DependsOn: []string{"worker_1", "worker_2"}},
				},
			},
			plan:      PlanPro,
			wantOrder: 4,
		},
		{
			name: "wait_for_event with valid timeout",
			spec: WorkflowSpec{
				Name: "event_flow",
				Steps: []WorkflowStepSpec{
					{Name: "init", Path: "/init"},
					{Name: "await", WaitForEvent: "email_verified", Timeout: 24 * time.Hour, DependsOn: []string{"init"}},
					{Name: "finalize", Path: "/finalize", DependsOn: []string{"await"}},
				},
			},
			plan:      PlanHobby,
			wantOrder: 3,
		},
		{
			name: "empty steps error",
			spec: WorkflowSpec{
				Name:  "empty",
				Steps: nil,
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowEmptySteps,
		},
		{
			name: "duplicate step name error",
			spec: WorkflowSpec{
				Name: "dup",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a"},
					{Name: "step_a", Path: "/dup"},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowDuplicateStep,
		},
		{
			name: "both path and wait_for_event error",
			spec: WorkflowSpec{
				Name: "conflict",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", WaitForEvent: "evt"},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowInvalidStepTarget,
		},
		{
			name: "neither path nor wait_for_event error",
			spec: WorkflowSpec{
				Name: "empty_target",
				Steps: []WorkflowStepSpec{
					{Name: "step_a"},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowInvalidStepTarget,
		},
		{
			name: "unknown dependency error",
			spec: WorkflowSpec{
				Name: "unknown_dep",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", DependsOn: []string{"non_existent"}},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowUnknownDependency,
		},
		{
			name: "self dependency error",
			spec: WorkflowSpec{
				Name: "self_dep",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", DependsOn: []string{"step_a"}},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowSelfDependency,
		},
		{
			name: "circular dependency error",
			spec: WorkflowSpec{
				Name: "cycle",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", DependsOn: []string{"step_c"}},
					{Name: "step_b", Path: "/b", DependsOn: []string{"step_a"}},
					{Name: "step_c", Path: "/c", DependsOn: []string{"step_b"}},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowDAGCycle,
		},
		{
			name: "timeout exceeds plan limit",
			spec: WorkflowSpec{
				Name: "too_long",
				Steps: []WorkflowStepSpec{
					{Name: "step_a", Path: "/a", Timeout: 30 * time.Minute},
				},
			},
			plan:    PlanHobby, // Hobby max is 10m
			wantErr: ErrWorkflowTimeoutExceeded,
		},
		{
			name: "unknown on_timeout step error",
			spec: WorkflowSpec{
				Name: "bad_timeout_target",
				Steps: []WorkflowStepSpec{
					{Name: "await", WaitForEvent: "evt", Timeout: time.Hour, OnTimeout: "ghost_step"},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowUnknownOnTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := ValidateWorkflowDAG(tt.spec, tt.plan)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error wrapping %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(order) != tt.wantOrder {
				t.Fatalf("expected %d steps in order, got %d", tt.wantOrder, len(order))
			}
		})
	}
}

func TestValidateWorkflowDAG_RejectsPlanAndPolicyViolations(t *testing.T) {
	tests := []struct {
		name    string
		spec    WorkflowSpec
		plan    Plan
		wantErr error
	}{
		{
			name: "free plan",
			spec: WorkflowSpec{
				Name:  "free",
				Steps: []WorkflowStepSpec{{Name: "main", Run: "do_work"}},
			},
			plan:    PlanFree,
			wantErr: ErrWorkflowPlanNotAllowed,
		},
		{
			name: "negative active timeout",
			spec: WorkflowSpec{
				Name:  "negative-timeout",
				Steps: []WorkflowStepSpec{{Name: "main", Run: "do_work", Timeout: -time.Second}},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowTimeoutInvalid,
		},
		{
			name: "subsecond event timeout",
			spec: WorkflowSpec{
				Name:  "short-wait",
				Steps: []WorkflowStepSpec{{Name: "wait", WaitForEvent: "ready", Timeout: 500 * time.Millisecond}},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowWaitTimeoutInvalid,
		},
		{
			name: "retry attempt zero",
			spec: WorkflowSpec{
				Name: "bad-retry",
				Steps: []WorkflowStepSpec{{
					Name: "main", Run: "do_work",
					Retry: &WorkflowRetrySpec{MaxAttempts: 0, Backoff: "fixed"},
				}},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowRetryInvalid,
		},
		{
			name: "duplicate dependency",
			spec: WorkflowSpec{
				Name: "duplicate-dependency",
				Steps: []WorkflowStepSpec{
					{Name: "first", Run: "first"},
					{Name: "second", Run: "second", DependsOn: []string{"first", "first"}},
				},
			},
			plan:    PlanHobby,
			wantErr: ErrWorkflowDuplicateDependency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateWorkflowDAG(tt.spec, tt.plan)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateWorkflowDAG_DeterministicOrder(t *testing.T) {
	spec := WorkflowSpec{
		Name: "stable-order",
		Steps: []WorkflowStepSpec{
			{Name: "zeta", Run: "zeta"},
			{Name: "alpha", Run: "alpha"},
			{Name: "middle", Run: "middle", DependsOn: []string{"alpha"}},
		},
	}
	want := []string{"alpha", "middle", "zeta"}
	for i := 0; i < 20; i++ {
		got, err := ValidateWorkflowDAG(spec, PlanHobby)
		if err != nil {
			t.Fatalf("ValidateWorkflowDAG: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
	}
}

func TestWorkflowSpecJSONWireShape(t *testing.T) {
	const payload = `{
  "name": "process_order",
  "trigger": {"type": "manual"},
  "steps": [{
    "name": "charge",
    "run": "charge_stripe",
    "input": {"order_id": "o-1"},
    "retry": {"max_attempts": 3, "backoff": "exponential"},
    "timeout": "30s"
  }]
}`

	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if spec.Trigger == nil || spec.Trigger.Type != "manual" {
		t.Fatalf("trigger = %+v, want manual", spec.Trigger)
	}
	if got := spec.Steps[0].Timeout; got != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", got)
	}
	var input map[string]string
	if err := json.Unmarshal(spec.Steps[0].Input, &input); err != nil || input["order_id"] != "o-1" {
		t.Fatalf("input = %s, want object containing order_id", spec.Steps[0].Input)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatalf("ValidateWorkflowDAG: %v", err)
	}

	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	encodedText := string(encoded)
	if !strings.Contains(encodedText, `"timeout":"30s"`) {
		t.Fatalf("encoded workflow = %s, want duration string", encodedText)
	}
	if !strings.Contains(encodedText, `"run":"charge_stripe"`) {
		t.Fatalf("encoded workflow = %s, want run field", encodedText)
	}

	var wait WorkflowSpec
	if err := json.Unmarshal([]byte(`{"name":"wait","steps":[{"name":"await","wait_for_event":"shipment_created","timeout":"7d"}]}`), &wait); err != nil {
		t.Fatalf("json.Unmarshal day timeout: %v", err)
	}
	if got := wait.Steps[0].Timeout; got != 7*24*time.Hour {
		t.Fatalf("day timeout = %v, want 168h", got)
	}
	if _, err := ValidateWorkflowDAG(wait, PlanHobby); err != nil {
		t.Fatalf("ValidateWorkflowDAG day timeout: %v", err)
	}
}

func TestWorkflowStepJSONRejectsUnknownField(t *testing.T) {
	var spec WorkflowSpec
	err := json.Unmarshal([]byte(`{"name":"bad","steps":[{"name":"main","run":"do_work","unknown":true}]}`), &spec)
	if err == nil || !strings.Contains(err.Error(), `unknown field "unknown"`) {
		t.Fatalf("error = %v, want nested unknown-field error", err)
	}
}

func TestWorkflowRetryJSONRejectsUnknownField(t *testing.T) {
	var spec WorkflowSpec
	err := json.Unmarshal([]byte(`{"name":"bad","steps":[{"name":"main","run":"do_work","retry":{"max_attempts":2,"jitter":"full"}}]}`), &spec)
	if err == nil || !strings.Contains(err.Error(), `unknown field "jitter"`) {
		t.Fatalf("error = %v, want nested retry unknown-field error", err)
	}
}
