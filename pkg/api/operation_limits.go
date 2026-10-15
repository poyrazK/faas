// adr: 1196
package api

// OperationLimits bounds one Operation. A zero field leaves that bound unset;
// each set field is bounded by the plan's OperationPlanLimits.
type OperationLimits struct {
	// DeadlineSeconds is wall clock from acceptance, including waits,
	// retries and recovery.
	DeadlineSeconds int `json:"deadline_seconds,omitempty" yaml:"deadline_seconds,omitempty" toml:"deadline_seconds,omitempty"`
	// MaxAttempts bounds the automatic attempts of one HTTP execution.
	MaxAttempts int `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty" toml:"max_attempts,omitempty"`
	// MaxExecutionSeconds bounds attempt runtime summed across generations.
	MaxExecutionSeconds int `json:"max_execution_seconds,omitempty" yaml:"max_execution_seconds,omitempty" toml:"max_execution_seconds,omitempty"`
}

// OperationUsage is what an Operation has consumed of its limits.
// ExecutionMS counts finished attempts; a running attempt is added when it ends.
type OperationUsage struct {
	ExecutionMS int64 `json:"execution_ms"`
	Attempts    int   `json:"attempts"`
}

// Failure codes of an Operation settled by its limits.
const (
	OperationFailureDeadlineExceeded = "deadline_exceeded"
	OperationFailureBudgetExhausted  = "budget_exhausted"
)
