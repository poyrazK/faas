package state

import (
	"context"
	"time"
)

// A terminal receipt records a private, verified cleanup commit. It is historical
// evidence, never authority to dispatch native work or delete another resource.
type ProjectEnvironmentCloneCompensationReceipt struct {
	Report      ProjectEnvironmentCloneCheckpointCompensationReconciliation
	CompletedAt time.Time
	SHA256      string
}

type ProjectEnvironmentCloneTerminalCompensationStore interface {
	CompleteProjectEnvironmentCloneCheckpointCompensation(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCompensationReceipt, error)
}

func completeCloneCompensationReport(r ProjectEnvironmentCloneCheckpointCompensationReconciliation) bool {
	if r.OperationID == "" || r.Revision <= 0 || !checkpointWorkloadRetirementID(r.EnvironmentID) || len(r.Blockers) != 0 {
		return false
	}
	for _, h := range []string{r.ResourcesSHA256, r.SourceRevisionHash, r.SourceCompensationSHA256, r.CaptureRetirementSHA256, r.OwnershipSHA256, r.NativePlanSHA256, r.NativeRetirementSHA256, r.ConfigurationCleanupSHA256, r.WorkloadCleanupSHA256, r.DeploymentCleanupSHA256, r.EnvironmentCleanupSHA256, r.WorkloadClosureSHA256} {
		if !validCloneObjectSHA256(h) {
			return false
		}
	}
	return true
}

func cloneCompensationReceiptSHA(op ProjectEnvironmentCloneOperation, tokenSHA string, r ProjectEnvironmentCloneCompensationReceipt) string {
	r.SHA256 = ""
	return cloneCheckpointSHA(struct {
		AccountID, ProjectID, SourceEnvironment, TargetEnvironment, SourceReleaseSetID, WorkerTokenSHA256 string
		Receipt                                                                                           ProjectEnvironmentCloneCompensationReceipt
	}{op.AccountID, op.ProjectID, op.SourceEnvironment, op.TargetEnvironment, op.SourceReleaseSetID, tokenSHA, r})
}
