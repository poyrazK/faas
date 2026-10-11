package api

// WorkflowRetryDecision is the shared retry policy for a failed workflow
// attempt.
type WorkflowRetryDecision struct {
	MaxAttempts int
	Retryable   bool
	ShouldRetry bool
	IsDead      bool
}

// WorkflowStepMaxAttempts returns the default or configured attempt limit for
// a workflow step. Unsafe outbound steps default to a single attempt.
func WorkflowStepMaxAttempts(spec WorkflowStepSpec) int {
	if spec.Retry != nil && spec.Retry.MaxAttempts > 0 {
		return spec.Retry.MaxAttempts
	}
	if spec.Outbound != nil && !spec.Outbound.SafeToRepeat() {
		return 1
	}
	return 3
}

// EvaluateWorkflowRetry applies the retry and terminal failure policy shared
// by workflow execution and simulation. attemptNumber is one-based. hasError
// represents a transport or execution error; otherwise httpStatus describes
// the failed response.
func EvaluateWorkflowRetry(spec WorkflowStepSpec, httpStatus int, hasError bool, attemptNumber int) WorkflowRetryDecision {
	maxAttempts := WorkflowStepMaxAttempts(spec)
	isDead := hasError || httpStatus >= 500
	retryable := isDead
	if spec.Outbound != nil {
		retryable = spec.Outbound.SafeToRepeat() && (retryable || httpStatus == 408 || httpStatus == 425 || httpStatus == 429)
	}

	return WorkflowRetryDecision{
		MaxAttempts: maxAttempts,
		Retryable:   retryable,
		ShouldRetry: retryable && attemptNumber > 0 && attemptNumber < maxAttempts,
		IsDead:      isDead,
	}
}
