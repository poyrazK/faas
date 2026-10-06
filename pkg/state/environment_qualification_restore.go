package state

import "context"

// A restore reservation belongs to the same reviewed attempt as its capture,
// but has a distinct instance, wake and cleanup capability. Admission is not
// native load, readiness, artifact ownership or activation evidence.
type EnvironmentQualificationRestoreAdmission struct {
	Instance  Instance
	Execution EnvironmentQualificationExecution
	Capture   EnvironmentQualificationSnapshotReceipt
	Created   bool
}

// Schedd reserves and marks a restore before any vmmd call. The original
// capture must already be physically retired; no second target or replay is
// allowed for an attempt. Cleanup uses EnvironmentQualificationExecutionStore.
type EnvironmentQualificationRestoreStore interface {
	CreateEnvironmentQualificationRestore(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentWorkloadQualificationPlacement) (EnvironmentQualificationRestoreAdmission, error)
	MarkEnvironmentQualificationRestoreDispatched(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationExecution) error
}

// Restore targets publish their runtime against the separate restore execution
// and original capture reservation. They never borrow the capture instance's
// runtime receipt or ordinary qualification publisher.
type EnvironmentQualificationRestoreRuntimeStore interface {
	PublishEnvironmentQualificationRestoreRuntime(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationExecution, EnvironmentWorkloadQualificationRuntime) (Instance, error)
}

func qualificationRestoreCaptureMatches(current EnvironmentWorkloadQualificationRequest, original EnvironmentQualificationExecutionStatus, capture EnvironmentQualificationSnapshotReceipt) bool {
	e := original.Execution
	return original.CaptureInstanceID == "" && original.DispatchStarted && original.RetiredAt != nil &&
		original.Retirement != nil && original.Retirement.Kind == QualificationNativeRetired &&
		e.InstanceID == current.ReservedInstanceID && e.RequestID == current.ID && e.Attempt == current.Attempt &&
		e == capture.Execution && e == qualificationExecution(current, Instance{ID: e.InstanceID, AppID: current.AppID,
		DeploymentID: current.DeploymentID, NodeID: e.NodeID, WakeID: e.WakeID, RAMMB: e.RAMMB}, e.CleanupToken) &&
		ValidateEnvironmentQualificationSnapshot(e, capture.Snapshot) == nil &&
		original.Retirement.NativeGeneration == capture.Snapshot.NativeGeneration && original.Retirement.KernelBootID == capture.Snapshot.KernelBootID
}

func qualificationRestoreAdmissionMatches(ins Instance, current EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) bool {
	copy := current
	copy.ReservedInstanceID = ins.ID
	return ins.ID != current.ReservedInstanceID && qualificationAdmissionMatches(ins, copy, placement)
}

func qualificationRestoreExecution(current EnvironmentWorkloadQualificationRequest, ins Instance, cleanup, captureID string) EnvironmentQualificationExecution {
	e := qualificationExecution(current, ins, cleanup)
	e.CaptureInstanceID = captureID
	return e
}
