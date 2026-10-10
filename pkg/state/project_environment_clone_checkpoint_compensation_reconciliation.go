package state

import "context"

// This private report is a lease-bound read of durable evidence, not terminal
// mutation authority. It neither dispatches native IO nor marks a stage complete.
// Whole-stage workload/configuration retirement must be implemented separately.
type ProjectEnvironmentCloneCheckpointCompensationReconciliation struct {
	OperationID, ResourcesSHA256, SourceRevisionHash  string
	Revision                                          int64
	SourceCompensationSHA256, CaptureRetirementSHA256 string
	Blockers                                          []ProjectEnvironmentCloneCheckpointCompensationBlocker
}

type ProjectEnvironmentCloneCheckpointCompensationBlocker struct {
	Kind, Name, TargetID, Reason string
}

type ProjectEnvironmentCloneCheckpointCompensationReconciliationStore interface {
	ReconcileProjectEnvironmentCloneCheckpointCompensation(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCompensationReconciliation, error)
}
