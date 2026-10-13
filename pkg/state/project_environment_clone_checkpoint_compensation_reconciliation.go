package state

import "context"

// This private report is a lease-bound read of durable evidence, not terminal
// mutation authority. It neither dispatches native IO nor marks a stage complete.
// WorkloadClosureSHA256 authenticates the complete modeled workload cleanup chain;
// unknown resources and published releases still require their owning protocols.
type ProjectEnvironmentCloneCheckpointCompensationReconciliation struct {
	OperationID, ResourcesSHA256, SourceRevisionHash  string
	Revision                                          int64
	SourceCompensationSHA256, CaptureRetirementSHA256 string
	EnvironmentID, OwnershipSHA256                    string
	NativePlanSHA256, NativeRetirementSHA256          string
	ConfigurationCleanupSHA256, WorkloadCleanupSHA256 string
	DeploymentCleanupSHA256, EnvironmentCleanupSHA256 string
	WorkloadClosureSHA256                             string
	Blockers                                          []ProjectEnvironmentCloneCheckpointCompensationBlocker
}

type ProjectEnvironmentCloneCheckpointCompensationBlocker struct {
	Kind, Name, TargetID, Reason string
}

type ProjectEnvironmentCloneCheckpointCompensationReconciliationStore interface {
	ReconcileProjectEnvironmentCloneCheckpointCompensation(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCompensationReconciliation, error)
}
