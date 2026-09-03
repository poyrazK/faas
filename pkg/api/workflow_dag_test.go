package api

import (
	"errors"
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
