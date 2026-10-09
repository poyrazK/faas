package api

import "testing"

func TestEvaluateWorkflowRetry(t *testing.T) {
	tests := []struct {
		name          string
		spec          WorkflowStepSpec
		httpStatus    int
		hasError      bool
		attemptNumber int
		maxAttempts   int
		retryable     bool
		shouldRetry   bool
		isDead        bool
	}{
		{
			name: "server error uses default attempts",
			spec: WorkflowStepSpec{}, httpStatus: 500, attemptNumber: 1,
			maxAttempts: 3, retryable: true, shouldRetry: true, isDead: true,
		},
		{
			name: "throttling retries within the attempt budget",
			spec: WorkflowStepSpec{}, httpStatus: 429, attemptNumber: 1,
			maxAttempts: 3, retryable: true, shouldRetry: true,
		},
		{
			name: "execution error retries and is dead",
			spec: WorkflowStepSpec{}, hasError: true, attemptNumber: 1,
			maxAttempts: 3, retryable: true, shouldRetry: true, isDead: true,
		},
		{
			name: "retry limit stops another attempt",
			spec: WorkflowStepSpec{Retry: &WorkflowRetrySpec{MaxAttempts: 2}}, httpStatus: 503, attemptNumber: 2,
			maxAttempts: 2, retryable: true, isDead: true,
		},
		{
			name: "safe outbound retries throttling status",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "GET"}}, httpStatus: 429, attemptNumber: 1,
			maxAttempts: 3, retryable: true, shouldRetry: true,
		},
		{
			name: "safe outbound stops at configured limit",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "GET"}, Retry: &WorkflowRetrySpec{MaxAttempts: 2}}, httpStatus: 408, attemptNumber: 2,
			maxAttempts: 2, retryable: true,
		},
		{
			name: "idempotent outbound retries transient status",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "POST", IdempotencySupported: true}, Retry: &WorkflowRetrySpec{MaxAttempts: 4}}, httpStatus: 425, attemptNumber: 1,
			maxAttempts: 4, retryable: true, shouldRetry: true,
		},
		{
			name: "unsafe outbound server error is dead but not retryable",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "POST"}}, httpStatus: 500, attemptNumber: 1,
			maxAttempts: 1, isDead: true,
		},
		{
			name: "unsafe outbound error is dead but not retryable",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "POST"}}, hasError: true, attemptNumber: 1,
			maxAttempts: 1, isDead: true,
		},
		{
			name: "outbound client error is failed",
			spec: WorkflowStepSpec{Outbound: &WorkflowOutboundSpec{Method: "GET"}}, httpStatus: 404, attemptNumber: 1,
			maxAttempts: 3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateWorkflowRetry(test.spec, test.httpStatus, test.hasError, test.attemptNumber)
			if decision.MaxAttempts != test.maxAttempts || decision.Retryable != test.retryable || decision.ShouldRetry != test.shouldRetry || decision.IsDead != test.isDead {
				t.Fatalf("EvaluateWorkflowRetry() = %+v, want max_attempts=%d retryable=%t should_retry=%t is_dead=%t", decision, test.maxAttempts, test.retryable, test.shouldRetry, test.isDead)
			}
		})
	}
}

func TestWorkflowTransientHTTPStatusesPreserveRetryBudget(t *testing.T) {
	for _, status := range []int{408, 425, 429} {
		decision := EvaluateWorkflowRetry(WorkflowStepSpec{}, status, false, 1)
		if !decision.ShouldRetry || decision.IsDead {
			t.Fatalf("status=%d decision=%+v", status, decision)
		}
		exhausted := EvaluateWorkflowRetry(WorkflowStepSpec{}, status, false, 3)
		if exhausted.ShouldRetry || exhausted.IsDead {
			t.Fatalf("exhausted status=%d decision=%+v", status, exhausted)
		}
	}
	for _, status := range []int{400, 401, 403, 404, 409, 422} {
		if decision := EvaluateWorkflowRetry(WorkflowStepSpec{}, status, false, 1); decision.ShouldRetry {
			t.Fatalf("permanent status=%d decision=%+v", status, decision)
		}
	}
}
