package state

import (
	"context"
	"maps"
	"reflect"
	"strconv"
	"time"
)

// EnvironmentQualificationRestoreReceipt records that one distinct target was
// restored from a retired capture, published with fresh runtime inputs, and
// natively retired. It does not attest smoke, readiness, activation or serving.
type EnvironmentQualificationRestoreReceipt struct {
	RequestID         string              `json:"request_id"`
	Attempt           int64               `json:"attempt"`
	CaptureInstanceID string              `json:"capture_instance_id"`
	InstanceID        string              `json:"instance_id"`
	Inputs            RuntimeConfigInputs `json:"inputs"`
	RecordedAt        time.Time           `json:"recorded_at"`
}

// Restore evidence is immutable and attempt-scoped. A successful read may be
// retained after the ephemeral target runtime receipt and VM are collected.
type EnvironmentQualificationRestoreReceiptStore interface {
	RecordEnvironmentQualificationRestoreReceipt(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationExecution, RuntimeConfigInputs) (EnvironmentQualificationRestoreReceipt, error)
	EnvironmentQualificationRestoreReceipt(context.Context, string, int64) (EnvironmentQualificationRestoreReceipt, error)
}

func normalizeQualificationRestoreInputs(inputs RuntimeConfigInputs) RuntimeConfigInputs {
	inputs.Boundary = inputs.Boundary.UTC().Truncate(time.Microsecond)
	return inputs
}

func qualificationRestoreReceiptKey(requestID string, attempt int64) string {
	return requestID + "/" + strconv.FormatInt(attempt, 10)
}

func qualificationClaimIdentityMatches(current, claimed EnvironmentWorkloadQualificationRequest) bool {
	return current.ID == claimed.ID && current.GraphID == claimed.GraphID && current.DeploymentID == claimed.DeploymentID &&
		current.AppID == claimed.AppID && current.Resource == claimed.Resource && current.Artifact == claimed.Artifact &&
		current.ExecutionMode == claimed.ExecutionMode && reflect.DeepEqual(current.FrozenInputs, claimed.FrozenInputs) &&
		current.ReservedInstanceID == claimed.ReservedInstanceID && current.Phase == "claimed" && current.Phase == claimed.Phase &&
		current.Attempt == claimed.Attempt && current.LeaseToken != "" && current.LeaseToken == claimed.LeaseToken && current.WorkerID == claimed.WorkerID
}

func qualificationRestoreReceiptMatchesRequest(receipt EnvironmentQualificationRestoreReceipt, claimed EnvironmentWorkloadQualificationRequest,
	frame EnvironmentQualificationExecution, inputs RuntimeConfigInputs) bool {
	return receipt.RequestID == claimed.ID && receipt.Attempt == claimed.Attempt && receipt.CaptureInstanceID == claimed.ReservedInstanceID &&
		receipt.InstanceID == frame.InstanceID && frame.CaptureInstanceID == claimed.ReservedInstanceID && runtimeConfigInputsEqual(receipt.Inputs, inputs)
}

// A restored target is prepared at its own input boundary. Values and versions
// must match the captured workload, while each receipt's boundary is checked
// independently for freshness.
func qualificationRuntimeValuesEqual(a, b RuntimeConfigInputs) bool {
	return a.Scope == b.Scope && a.AllSecrets == b.AllSecrets && maps.Equal(a.Variables, b.Variables) &&
		maps.Equal(a.SecretVersions, b.SecretVersions) && maps.Equal(a.SecretRefs, b.SecretRefs) &&
		maps.Equal(a.SidecarSecretVersions, b.SidecarSecretVersions)
}

func qualificationRestoreReceiptValid(request EnvironmentWorkloadQualificationRequest, original EnvironmentQualificationExecutionStatus,
	capture EnvironmentQualificationSnapshotReceipt, target EnvironmentQualificationExecutionStatus, inputs RuntimeConfigInputs) bool {
	frame := target.Execution
	retirement := target.Retirement
	if request.ID == "" || request.Attempt < 1 || request.ReservedInstanceID == "" ||
		!qualificationRestoreCaptureMatches(request, original, capture) ||
		target.CaptureInstanceID != capture.Execution.InstanceID || !target.DispatchStarted || target.RetiredAt == nil || retirement == nil ||
		!qualificationRetirementValid(*retirement, true) || retirement.Kind != QualificationNativeRetired ||
		frame.InstanceID == capture.Execution.InstanceID || frame.NodeID != capture.Execution.NodeID || frame.RAMMB != capture.Execution.RAMMB ||
		frame != qualificationRestoreExecution(request, Instance{ID: frame.InstanceID, AppID: request.AppID, DeploymentID: request.DeploymentID,
			NodeID: frame.NodeID, WakeID: frame.WakeID, RAMMB: frame.RAMMB}, frame.CleanupToken, capture.Execution.InstanceID) ||
		retirement.NativeGeneration == capture.Snapshot.NativeGeneration || retirement.KernelBootID != capture.Snapshot.KernelBootID ||
		original.Retirement == nil || retirement.ReceiptID == original.Retirement.ReceiptID ||
		validateRuntimeConfigInputs(inputs) != nil || !qualificationRuntimeValuesEqual(inputs, capture.Inputs) {
		return false
	}
	return true
}

func cloneQualificationRestoreReceipt(receipt EnvironmentQualificationRestoreReceipt) EnvironmentQualificationRestoreReceipt {
	receipt.Inputs = cloneRuntimeConfigInputs(receipt.Inputs)
	return receipt
}
