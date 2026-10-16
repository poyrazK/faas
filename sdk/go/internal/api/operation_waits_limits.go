package api

import "time"

// Mirrors pkg/api/operation_limits.go (ADR-1196) and the wire types of
// pkg/api/operation_waits.go (ADR-1194) that operations.go references.

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

// OperationApprovalSpec declares that a workflow callback step is an approval
// decided by the account or by the operation's customer.
type OperationApprovalSpec struct {
	Step     string `json:"step" yaml:"step" toml:"step"`
	Approver string `json:"approver" yaml:"approver" toml:"approver"`
}

// OperationWaitingState describes why a waiting operation holds no execution.
// Until is the timer's wake time or the approval deadline, when one exists.
type OperationWaitingState struct {
	Kind     string     `json:"kind"`
	Step     string     `json:"step"`
	Since    time.Time  `json:"since"`
	Until    *time.Time `json:"until,omitempty"`
	Approver string     `json:"approver,omitempty"`
	// Event names the signal an event wait expects (ADR-1197).
	Event string `json:"event,omitempty"`
}
