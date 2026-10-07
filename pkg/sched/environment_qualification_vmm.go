package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// This additive capability must fence the complete immutable frame on the
// recorded host. Retire revokes every producer, including a delayed boot RPC,
// and returns durable native process/resource evidence for that same frame.
// Older vmmd nodes and generic Destroy cannot implement this contract.
type EnvironmentQualificationVMM interface {
	CreateEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution, AppSpec) (*WakeOutcome, error)
	RetireEnvironmentQualification(context.Context, state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error)
}

type EnvironmentQualificationRetirementEvidence struct {
	Execution  state.EnvironmentQualificationExecution
	Retirement state.EnvironmentQualificationRetirement
}
