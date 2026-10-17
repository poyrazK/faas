package deploystaging

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type MigrationContractStore interface {
	state.ProjectMigrationContractStore
	state.ProjectSubmissionStore
	state.ProjectReleaseSetReader
	state.RuntimeAppValuesStore
}

// Contract admission probes the current graph, never an expired rollback graph.
// The state transaction repeats ownership and drain barriers after external I/O.
func AdmitProjectMigrationContract(ctx context.Context, store MigrationContractStore, account, attempt string, req api.ProjectReleaseControlRequest, check func(context.Context, []state.ProjectReleaseMember) error, probe func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)) (*api.ProjectMigrationContract, error) {
	status, err := store.ProjectMigrationContractStatus(ctx, account, attempt)
	if err != nil {
		return nil, err
	}
	if status.TaskID != "" {
		return store.AdmitProjectMigrationContract(ctx, account, attempt, req, state.ProjectMigrationContractEvidence{})
	}
	if status.Status != "ready" {
		return nil, state.ErrConflict
	}
	receipt, err := store.ProjectSubmission(ctx, account, attempt)
	if err != nil {
		return nil, err
	}
	active, err := store.ActiveProjectReleaseSet(ctx, account, receipt.Apply.ProjectID, "production")
	if err != nil {
		return nil, err
	}
	if receipt.Apply.SafeRelease == nil || active.ID != receipt.Apply.SafeRelease.ReleaseID || check == nil || probe == nil {
		return nil, state.ErrConflict
	}
	if err = check(ctx, active.Members); err != nil {
		return nil, err
	}
	deployment := ""
	for _, m := range active.Members {
		if m.AppID == receipt.Apply.SafeRelease.ReleaseCommand.AppID {
			deployment = m.DeploymentID
		}
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, account, receipt.Apply.SafeRelease.ReleaseCommand.AppID, deployment)
	if err != nil {
		return nil, err
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		return nil, err
	}
	evidence := state.ProjectMigrationContractEvidence{StartedAt: time.Now().UTC(), RuntimeFingerprint: fence.Fingerprint}
	evidence.Evidence, err = probe(ctx, receipt, active)
	if err != nil {
		return nil, err
	}
	return store.AdmitProjectMigrationContract(ctx, account, attempt, req, evidence)
}
