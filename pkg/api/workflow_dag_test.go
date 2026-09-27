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
			name: "three-day durable timer",
			spec: WorkflowSpec{
				Name: "order",
				Steps: []WorkflowStepSpec{
					{Name: "charge", Run: "charge"},
					{Name: "wait", WaitForDuration: 3 * 24 * time.Hour, DependsOn: []string{"charge"}},
					{Name: "check_delivery", Run: "check_delivery", DependsOn: []string{"wait"}},
				},
			},
			plan:      PlanHobby,
			wantOrder: 3,
		},
		{
			name: "callback wait",
			spec: WorkflowSpec{Name: "approval", Steps: []WorkflowStepSpec{
				{Name: "request", Run: "request_approval"},
				{Name: "await", WaitForCallback: true, Timeout: 3 * 24 * time.Hour, DependsOn: []string{"request"}},
				{Name: "continue", Run: "continue_order", DependsOn: []string{"await"}},
			}},
			plan: PlanHobby, wantOrder: 3,
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
			name: "callback requires timeout",
			spec: WorkflowSpec{Name: "callback-no-timeout", Steps: []WorkflowStepSpec{
				{Name: "await", WaitForCallback: true},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowWaitTimeoutInvalid,
		},
		{
			name: "callback cannot run handler",
			spec: WorkflowSpec{Name: "callback-with-handler", Steps: []WorkflowStepSpec{
				{Name: "await", WaitForCallback: true, Run: "handler", Timeout: time.Hour},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowInvalidStepTarget,
		},
		{
			name: "callback cannot retry",
			spec: WorkflowSpec{Name: "callback-with-retry", Steps: []WorkflowStepSpec{
				{Name: "await", WaitForCallback: true, Timeout: time.Hour, Retry: &WorkflowRetrySpec{MaxAttempts: 2}},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowCallbackOptionsInvalid,
		},
		{
			name: "callback event namespace reserved",
			spec: WorkflowSpec{Name: "reserved-event", Steps: []WorkflowStepSpec{
				{Name: "await", WaitForEvent: "workflow.callback.fake", Timeout: time.Hour},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowReservedEventName,
		},
		{
			name: "subsecond timer",
			spec: WorkflowSpec{Name: "short-timer", Steps: []WorkflowStepSpec{
				{Name: "wait", WaitForDuration: 500 * time.Millisecond},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowWaitDurationInvalid,
		},
		{
			name: "timer exceeds plan cap",
			spec: WorkflowSpec{Name: "long-timer", Steps: []WorkflowStepSpec{
				{Name: "wait", WaitForDuration: 31 * 24 * time.Hour},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowWaitDurationInvalid,
		},
		{
			name: "timer cannot have active timeout",
			spec: WorkflowSpec{Name: "mixed-timer", Steps: []WorkflowStepSpec{
				{Name: "wait", WaitForDuration: time.Hour, Timeout: time.Second},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowWaitOptionsInvalid,
		},
		{
			name: "timer cannot have another target",
			spec: WorkflowSpec{Name: "mixed-target", Steps: []WorkflowStepSpec{
				{Name: "wait", WaitForDuration: time.Hour, Run: "charge"},
			}},
			plan: PlanHobby, wantErr: ErrWorkflowInvalidStepTarget,
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

func TestWorkflowDurationWaitJSONRoundTrip(t *testing.T) {
	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(`{"name":"order","steps":[{"name":"wait","wait_for_duration":"3d"}]}`), &spec); err != nil {
		t.Fatalf("unmarshal timer: %v", err)
	}
	if got := spec.Steps[0].WaitForDuration; got != 72*time.Hour {
		t.Fatalf("wait duration = %s, want 72h", got)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatalf("validate timer: %v", err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal timer: %v", err)
	}
	var roundTrip WorkflowSpec
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if got := roundTrip.Steps[0].WaitForDuration; got != 72*time.Hour {
		t.Fatalf("round-trip wait duration = %s, want 72h", got)
	}
}

func TestWorkflowCallbackJSONAndIdentifiers(t *testing.T) {
	var spec WorkflowSpec
	if err := json.Unmarshal([]byte(`{"name":"approval","steps":[{"name":"await","wait_for_callback":true,"timeout":"3d"}]}`), &spec); err != nil {
		t.Fatal(err)
	}
	if !spec.Steps[0].WaitForCallback || spec.Steps[0].Timeout != 72*time.Hour {
		t.Fatalf("callback spec = %#v", spec.Steps[0])
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || !strings.Contains(string(encoded), `"wait_for_callback":true`) {
		t.Fatalf("encoded callback = %s, err = %v", encoded, err)
	}
	one := WorkflowCallbackID("run-1", "await")
	if one != WorkflowCallbackID("run-1", "await") || one == WorkflowCallbackID("run-2", "await") || one == WorkflowCallbackID("run-1", "other") {
		t.Fatal("callback IDs are not stable and run/step scoped")
	}
	if !IsWorkflowCallbackEventName(WorkflowCallbackEventName("run-1", "await")) {
		t.Fatal("callback event did not use reserved namespace")
	}
}

func TestWorkflowConditionWireAndValidation(t *testing.T) {
	var spec WorkflowSpec
	input := `{"name":"delivery","steps":[{"name":"await_delivery","wait_for_condition":{"run":"check_delivery","interval":"30m","max_attempts":100},"timeout":"3d"}]}`
	if err := json.Unmarshal([]byte(input), &spec); err != nil {
		t.Fatal(err)
	}
	condition := spec.Steps[0].WaitForCondition
	if condition == nil || condition.Run != "check_delivery" || condition.Interval != 30*time.Minute || condition.MaxAttempts != 100 {
		t.Fatalf("decoded condition = %#v", condition)
	}
	if _, err := ValidateWorkflowDAG(spec, PlanHobby); err != nil {
		t.Fatalf("valid condition: %v", err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil || !strings.Contains(string(encoded), `"interval":"30m0s"`) {
		t.Fatalf("condition round trip = %s, %v", encoded, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*WorkflowStepSpec)
		want   error
	}{
		{"short interval", func(s *WorkflowStepSpec) { s.WaitForCondition.Interval = time.Second }, ErrWorkflowConditionInvalid},
		{"long interval", func(s *WorkflowStepSpec) { s.WaitForCondition.Interval = 8 * 24 * time.Hour }, ErrWorkflowConditionInvalid},
		{"too many attempts", func(s *WorkflowStepSpec) { s.WaitForCondition.MaxAttempts = 1001 }, ErrWorkflowConditionInvalid},
		{"missing checker", func(s *WorkflowStepSpec) { s.WaitForCondition.Run = "" }, ErrWorkflowConditionInvalid},
		{"missing timeout", func(s *WorkflowStepSpec) { s.Timeout = 0 }, ErrWorkflowConditionTimeoutInvalid},
		{"long timeout", func(s *WorkflowStepSpec) { s.Timeout = 8 * 24 * time.Hour }, ErrWorkflowConditionTimeoutInvalid},
		{"mixed target", func(s *WorkflowStepSpec) { s.Run = "other" }, ErrWorkflowInvalidStepTarget},
		{"retry option", func(s *WorkflowStepSpec) { s.Retry = &WorkflowRetrySpec{MaxAttempts: 2} }, ErrWorkflowConditionOptionsInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copySpec := spec
			copySpec.Steps = append([]WorkflowStepSpec(nil), spec.Steps...)
			copySpec.Steps[0].WaitForCondition = &WorkflowConditionSpec{Run: condition.Run, Interval: condition.Interval, MaxAttempts: condition.MaxAttempts}
			tc.mutate(&copySpec.Steps[0])
			if _, err := ValidateWorkflowDAG(copySpec, PlanHobby); !errors.Is(err, tc.want) {
				t.Fatalf("validation = %v, want %v", err, tc.want)
			}
		})
	}
	if err := json.Unmarshal([]byte(`{"name":"bad","steps":[{"name":"wait","wait_for_condition":{"run":"check","interval":"1m","max_attempts":2,"unexpected":true},"timeout":"1h"}]}`), &spec); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown nested condition field = %v", err)
	}
}

func TestWorkflowRetryJSONRejectsUnknownField(t *testing.T) {
	var spec WorkflowSpec
	err := json.Unmarshal([]byte(`{"name":"bad","steps":[{"name":"main","run":"do_work","retry":{"max_attempts":2,"jitter":"full"}}]}`), &spec)
	if err == nil || !strings.Contains(err.Error(), `unknown field "jitter"`) {
		t.Fatalf("error = %v, want nested retry unknown-field error", err)
	}
}
