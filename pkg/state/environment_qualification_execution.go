package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Execution is the immutable placement and cleanup capability of one attempt.
// It survives removal of the source/request; cleanup uses the recorded node,
// never the app's current owner. It grants no new boot or serving authority.
type EnvironmentQualificationExecution struct {
	InstanceID string `json:"instance_id"`
	// Restore targets cannot pass through the capture producer's boot protocol.
	CaptureInstanceID string                      `json:"capture_instance_id,omitempty"`
	RequestID         string                      `json:"request_id"`
	GraphID           string                      `json:"graph_id"`
	AppID             string                      `json:"app_id"`
	DeploymentID      string                      `json:"deployment_id"`
	NodeID            string                      `json:"node_id"`
	WakeID            string                      `json:"wake_id"`
	SourceID          string                      `json:"source_id"`
	EnvironmentID     string                      `json:"environment_id"`
	RevisionID        string                      `json:"revision_id"`
	Resource          string                      `json:"resource"`
	Scope             string                      `json:"scope"`
	PlanHash          string                      `json:"plan_hash"`
	Generation        int64                       `json:"generation"`
	IntentVersion     int64                       `json:"intent_version"`
	Attempt           int64                       `json:"attempt"`
	RAMMB             int                         `json:"ram_mb"`
	Artifact          EnvironmentWorkloadArtifact `json:"artifact"`
	CleanupToken      string                      `json:"-"`
}

// Cleanup authority must stay out of diagnostic formatting as well as JSON.
func (e EnvironmentQualificationExecution) String() string {
	return fmt.Sprintf("qualification=%s attempt=%d instance=%s node=%s wake=%s", e.RequestID, e.Attempt, e.InstanceID, e.NodeID, e.WakeID)
}

func (e EnvironmentQualificationExecution) GoString() string { return e.String() }

type EnvironmentQualificationExecutionStatus struct {
	Execution EnvironmentQualificationExecution
	// Empty for capture producers; otherwise the retained original capture VM.
	CaptureInstanceID string
	DispatchStarted   bool
	Retirement        *EnvironmentQualificationRetirement
	RetiredAt         *time.Time
}

// Native evidence must come from the attempt-aware vmmd operation, after all
// native producers have been revoked and both processes and resources joined.
// Generic Destroy success, NotFound, or a terminal instance is not evidence.
type EnvironmentQualificationRetirement struct {
	Kind             string `json:"kind"`
	ReceiptID        string `json:"receipt_id,omitempty"`
	NativeGeneration string `json:"native_generation,omitempty"`
	KernelBootID     string `json:"kernel_boot_id,omitempty"`
	ProcessesExited  bool   `json:"processes_exited,omitempty"`
	ResourcesRemoved bool   `json:"resources_removed,omitempty"`
}

const (
	QualificationNeverDispatched = "never_dispatched"
	QualificationNativeRetired   = "native_retired"
)

// Schedd alone consumes this capability. Dispatch is durably marked BEFORE
// invoking vmmd. Retirement remains available after expiry or supersession.
type EnvironmentQualificationExecutionStore interface {
	EnvironmentQualificationExecution(context.Context, string) (EnvironmentQualificationExecutionStatus, error)
	MarkEnvironmentQualificationDispatched(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationExecution) error
	RetireEnvironmentQualificationExecution(context.Context, EnvironmentQualificationExecution, EnvironmentQualificationRetirement) error
}

// Discovery is advisory. Recheck eligibility under the request/frame locks
// before touching the native host: a renewal may have committed since listing.
// The expired original lease cannot be renewed, and a new attempt cannot reuse
// its held frame. This grants cleanup authority only, never boot authority.
type EnvironmentQualificationRecoveryStore interface {
	ListEnvironmentQualificationExecutionsForRecovery(context.Context, string, string, int) ([]EnvironmentQualificationExecutionStatus, error)
	EnvironmentQualificationExecutionForRecovery(context.Context, string, string) (EnvironmentQualificationExecutionStatus, error)
}

func qualificationRecoveryUUIDValid(id string) bool {
	parsed, err := uuid.Parse(id)
	// MemStore uses the same UUID bytes without separators for node IDs.
	return err == nil && parsed != uuid.Nil && (parsed.String() == id || strings.ReplaceAll(parsed.String(), "-", "") == id)
}

func qualificationRecoveryCursor(id string) string { return strings.ReplaceAll(id, "-", "") }

func qualificationRecoveryPageValid(nodeID, afterInstanceID string, limit int) bool {
	return qualificationRecoveryUUIDValid(nodeID) && (afterInstanceID == "" || qualificationRecoveryUUIDValid(afterInstanceID)) &&
		limit > 0 && limit <= api.EnvironmentGitOpsQualificationRecoveryBatchMax
}

func qualificationExecutionHasActiveLease(status EnvironmentQualificationExecutionStatus, request EnvironmentWorkloadQualificationRequest, now time.Time) bool {
	reservation := status.Execution.InstanceID
	if status.CaptureInstanceID != "" {
		reservation = status.CaptureInstanceID
	}
	return request.ID == status.Execution.RequestID && request.Attempt == status.Execution.Attempt &&
		request.ReservedInstanceID == reservation && request.Phase == "claimed" && request.LeaseUntil != nil && now.Before(*request.LeaseUntil)
}

func qualificationExecution(request EnvironmentWorkloadQualificationRequest, ins Instance, cleanupToken string) EnvironmentQualificationExecution {
	f := request.FrozenInputs
	return EnvironmentQualificationExecution{InstanceID: ins.ID, RequestID: request.ID, GraphID: request.GraphID, AppID: ins.AppID,
		DeploymentID: ins.DeploymentID, NodeID: ins.NodeID, WakeID: ins.WakeID, SourceID: f.SourceID, EnvironmentID: f.EnvironmentID,
		RevisionID: f.RevisionID, Resource: request.Resource, Scope: f.Scope, PlanHash: f.PlanHash, Generation: f.Generation,
		IntentVersion: f.IntentVersion, Attempt: request.Attempt, RAMMB: ins.RAMMB, Artifact: request.Artifact, CleanupToken: cleanupToken}
}

func qualificationExecutionMatches(a, b EnvironmentQualificationExecution) bool { return a == b }

func qualificationRetirementValid(proof EnvironmentQualificationRetirement, dispatched bool) bool {
	if !dispatched {
		return proof == (EnvironmentQualificationRetirement{Kind: QualificationNeverDispatched})
	}
	if proof.Kind != QualificationNativeRetired || !proof.ProcessesExited || !proof.ResourcesRemoved {
		return false
	}
	for _, id := range []string{proof.ReceiptID, proof.NativeGeneration, proof.KernelBootID} {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
			return false
		}
	}
	return true
}

func cloneQualificationExecutionStatus(status EnvironmentQualificationExecutionStatus) EnvironmentQualificationExecutionStatus {
	if status.Retirement != nil {
		copy := *status.Retirement
		status.Retirement = &copy
	}
	if status.RetiredAt != nil {
		copy := *status.RetiredAt
		status.RetiredAt = &copy
	}
	return status
}

func qualificationRetirementEqual(a *EnvironmentQualificationRetirement, b EnvironmentQualificationRetirement) bool {
	return a != nil && *a == b
}

func qualificationExecutionStateCharged(state State) bool {
	return state == StateColdBooting || state == StateWaking || state == StateRunning || state == StateDraining || state == StateWarm
}

func qualificationRetiredState(state State) (State, bool) {
	if qualificationInstanceRetired(Instance{State: string(state)}) || state == StateEvictingAccountDeleting {
		return state, true
	}
	return StateStopped, CanTransition(state, StateStopped)
}
