package deploystaging

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type MigrationRecoveryStore interface {
	MigrationContractStore
	state.ProjectMigrationRecoveryStore
}

func ResolveProjectMigrationContract(ctx context.Context, store MigrationRecoveryStore, account, attempt string, req api.ProjectMigrationRecoveryRequest, check func(context.Context, []state.ProjectReleaseMember) error, probe func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)) (*api.ProjectMigrationRecovery, error) {
	if !req.Valid() || req.ReplacementAttemptID == attempt {
		return nil, state.ErrInvalidArgument
	}
	status, err := store.ProjectMigrationContractStatus(ctx, account, attempt)
	if err != nil {
		return nil, err
	}
	if status.Recovery != nil && status.Recovery.Status == "resolved" {
		return store.ResolveProjectMigrationContract(ctx, account, attempt, req, state.ProjectMigrationRecoveryEvidence{})
	}
	if status.Recovery == nil || status.Recovery.Status != "needs_operator" || check == nil || probe == nil {
		return nil, state.ErrConflict
	}
	original, err := store.ProjectSubmission(ctx, account, attempt)
	if err != nil {
		return nil, err
	}
	replacement, err := store.ProjectSubmission(ctx, account, req.ReplacementAttemptID)
	if err != nil {
		return nil, err
	}
	if replacement.Apply.ProjectID != original.Apply.ProjectID || replacement.Apply.SafeRelease == nil || replacement.Apply.SafeRelease.Verification == nil {
		return nil, state.ErrConflict
	}
	active, err := store.ActiveProjectReleaseSet(ctx, account, original.Apply.ProjectID, "production")
	if err != nil {
		return nil, err
	}
	if active.ID != replacement.Apply.SafeRelease.ReleaseID {
		return nil, state.ErrConflict
	}
	if err = check(ctx, active.Members); err != nil {
		return nil, err
	}
	evidence := state.ProjectMigrationRecoveryEvidence{StartedAt: time.Now().UTC(), RuntimeFingerprints: map[string]string{}}
	for _, member := range active.Members {
		values, err := store.RuntimeAppValuesForDeployment(ctx, account, member.AppID, member.DeploymentID)
		if err != nil {
			return nil, err
		}
		fence, err := state.NewRuntimeAppConfigFence(values)
		if err != nil {
			return nil, err
		}
		evidence.RuntimeFingerprints[member.AppID] = fence.Fingerprint
	}
	evidence.Probes, err = probe(ctx, replacement, active)
	if err != nil {
		return nil, err
	}
	return store.ResolveProjectMigrationContract(ctx, account, attempt, req, evidence)
}
