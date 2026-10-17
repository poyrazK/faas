package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func releaseWorkerCaptureDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, baselineID string) (*api.ProjectReleaseWorker, error) {
	rows, err := sqlc.New().ReadProjectReleaseWorkerCapture(ctx, db, sqlc.ReadProjectReleaseWorkerCaptureParams{AccountID: accountID, ProjectID: projectID, BaselineID: baselineID})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrConflict
	}
	var capture projectWorkerCapture
	if err := json.Unmarshal(rows[0], &capture); err != nil {
		return nil, err
	}
	return capture.policy()
}
func (s *PgStore) PreviewProjectReleaseWorker(ctx context.Context, accountID, projectID, baselineID string) (*api.ProjectReleaseWorker, error) {
	worker, err := releaseWorkerCaptureDB(ctx, s.pool, accountID, projectID, baselineID)
	if err != nil {
		return nil, err
	}
	return validatePreviousProjectWorker(ctx, s, worker)
}
func validateReleaseWorkerCandidateDB(ctx context.Context, db sqlc.DBTX, d Deployment) error {
	row, err := sqlc.New().ReleaseWorkerCandidateOwner(ctx, db, sqlc.ReleaseWorkerCandidateOwnerParams{AppID: d.AppID, AttemptID: d.releaseWorkerAttempt})
	if err != nil {
		return mapErr(err)
	}
	var receipt api.ProjectSubmissionReceipt
	if err := json.Unmarshal(row.Payload, &receipt); err != nil {
		return err
	}
	receipt.Status = row.Status
	if !workerReceiptCandidate(receipt, d) {
		return ErrConflict
	}
	owner, err := sqlc.New().ReleaseConfigurationCandidateOwner(ctx, db, d.AppID)
	if err != nil {
		return err
	}
	current, err := releaseWorkerCaptureDB(ctx, db, owner.AccountID, owner.ProjectID, receipt.Apply.SafeRelease.PreviousReleaseID)
	if err != nil || api.ProjectReleaseWorkerPolicyID(current) != api.ProjectReleaseWorkerPolicyID(receipt.Apply.SafeRelease.Worker) {
		return ErrConflict
	}
	return nil
}
func pinReleaseWorkerCandidateDB(ctx context.Context, db sqlc.DBTX, d Deployment, attempt string) error {
	return sqlc.New().PinProjectReleaseWorkerCandidate(ctx, db, sqlc.PinProjectReleaseWorkerCandidateParams{DeploymentID: d.ID, AttemptID: attempt})
}
func (s *PgStore) HeldProjectReleaseWorkers(ctx context.Context, appID string) ([]string, error) {
	return sqlc.New().HeldProjectReleaseWorkers(ctx, s.pool, appID)
}
func guardReleaseWorkerDB(ctx context.Context, db sqlc.DBTX, accountID string, r api.ProjectSubmissionReceipt, afterCutover bool) (bool, error) {
	worker := r.Apply.SafeRelease.Worker
	if worker == nil {
		return true, nil
	}
	baselineID := r.Apply.SafeRelease.PreviousReleaseID
	if afterCutover {
		baselineID = r.Apply.SafeRelease.ReleaseID
	}
	current, err := releaseWorkerCaptureDB(ctx, db, accountID, r.Apply.ProjectID, baselineID)
	if err != nil {
		return false, err
	}
	current.PreviousDeploymentID = worker.PreviousDeploymentID
	if api.ProjectReleaseWorkerPolicyID(current) != api.ProjectReleaseWorkerPolicyID(worker) {
		return false, ErrConflict
	}
	depID := selectedProjectWorkerDeployment(r)
	if depID == "" {
		return false, nil
	}
	owned, err := sqlc.New().ProjectReleaseWorkerCandidateMatches(ctx, db, sqlc.ProjectReleaseWorkerCandidateMatchesParams{DeploymentID: depID, AccountID: accountID, AttemptID: r.AttemptID})
	if err != nil {
		return false, err
	}
	if !owned {
		return false, ErrConflict
	}
	worker.DeploymentID = depID
	worker.InstanceID, err = releaseWorkerReadyDB(ctx, db, accountID, worker.AppID, depID)
	if err != nil {
		return false, err
	}
	worker.Status = "building"
	if worker.InstanceID != "" {
		worker.Status = "ready"
		if afterCutover {
			worker.Status = "active"
		}
	}
	return worker.InstanceID != "", nil
}

func releaseWorkerReadyDB(ctx context.Context, db sqlc.DBTX, accountID, appID, depID string) (string, error) {
	proofs, err := sqlc.New().ReadReleaseWorkerRunningProof(ctx, db, depID)
	if err != nil {
		return "", err
	}
	if len(proofs) == 0 {
		return "", nil
	}
	snapshot, err := runtimeAppValuesDB(ctx, db, accountID, appID, depID)
	if err != nil {
		return "", err
	}
	expected, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		return "", err
	}
	for _, proof := range proofs {
		if proof.Scope == expected.SecretFence.Scope && proof.SecretFingerprint == expected.SecretFence.Fingerprint && proof.ConfigFingerprint == expected.Fingerprint && proof.EnvironmentID == expected.SecretFence.EnvironmentID {
			return proof.ID, nil
		}
	}
	return "", nil
}
