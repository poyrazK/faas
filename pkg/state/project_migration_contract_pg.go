package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func migrationContractFromRow(row sqlc.ReadMigrationContractRow) projectMigrationContract {
	return projectMigrationContract{ProjectID: row.ProjectID, AppID: row.AppID, Command: row.Command, CommandHash: row.CommandHash, ExpansionTaskID: row.ExpansionTaskID, RuntimeFingerprint: row.RuntimeFingerprint, PreparedLeaseToken: row.PreparedLeaseToken, TaskID: row.TaskID, RequestID: row.RequestID, Generation: uint64(row.Generation), ReleaseID: row.ReleaseID, ConfigVersion: row.ConfigVersion, ConfigHash: row.ConfigHash, RetiredIDs: row.RetiredIds}
}
func (s *PgStore) ProjectMigrationDeploymentRetired(ctx context.Context, id string) (bool, error) {
	return sqlc.New().MigrationDeploymentRetired(ctx, s.pool, id)
}
func (s *PgStore) StageProjectMigrationContract(ctx context.Context, account, attempt, project, command string) error {
	if _, problem := (api.CreateAppTaskRequest{Command: []string{command}, CommandShell: true}).Resolve(); problem != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return mapErr(err)
	}
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(row.Payload, &r) != nil || r.Apply.SafeRelease == nil || r.Apply.SafeRelease.ReleaseCommand == nil || !r.Apply.SafeRelease.ReleaseCommand.VerifyBaseline {
		return ErrConflict
	}
	if err = q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: project, AccountID: account}); err != nil {
		return err
	}
	n, err := q.StageMigrationContract(ctx, tx, sqlc.StageMigrationContractParams{AccountID: account, AttemptID: attempt, ProjectID: project, AppID: r.Apply.SafeRelease.ReleaseCommand.AppID, Command: []string{command}, Hash: api.ProjectReleaseCommandHash([]string{command}, true), WorkloadAttempt: ProjectWorkloadAttempt(attempt, r.Apply.SafeRelease.ReleaseCommand.AppID)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func migrationContractTaskStatusDB(ctx context.Context, db sqlc.DBTX, c projectMigrationContract) (*api.ProjectMigrationContract, error) {
	q := sqlc.New()
	recovery, recoveryErr := q.ReadMigrationRecoveryForTask(ctx, db, c.TaskID)
	if recoveryErr == nil {
		c.Recovery = recoveryFromRow(sqlc.ReadMigrationRecoveryRow(recovery))
	} else if !errors.Is(mapErr(recoveryErr), ErrNotFound) {
		return nil, recoveryErr
	}
	row, err := q.ReadMigrationContractTask(ctx, db, c.TaskID)
	if errors.Is(mapErr(err), ErrNotFound) {
		return contractTaskResult(c, AppTask{}, false, false), nil
	}
	if err != nil {
		return nil, err
	}
	task, err := appTaskFromSQLC(row)
	if err != nil {
		return nil, err
	}
	allowed, err := q.MigrationTaskAllowed(ctx, db, c.TaskID)
	if err != nil {
		return nil, err
	}
	if allowed && !task.Status.Terminal() {
		allowed = guardMigrationTaskRuntimeDB(ctx, db, task) == nil
	}
	return contractTaskResult(c, task, true, allowed), nil
}
func (s *PgStore) ProjectMigrationContractStatus(ctx context.Context, account, attempt string) (*api.ProjectMigrationContract, error) {
	q := sqlc.New()
	row, err := q.ReadMigrationContract(ctx, s.pool, sqlc.ReadMigrationContractParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return nil, mapErr(err)
	}
	c := migrationContractFromRow(row)
	if c.TaskID != "" {
		return migrationContractTaskStatusDB(ctx, s.pool, c)
	}
	entry, err := q.ReadProjectSubmission(ctx, s.pool, sqlc.ReadProjectSubmissionParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return nil, mapErr(err)
	}
	var r api.ProjectSubmissionReceipt
	if err = json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	reason := contractPlanReason(r.Apply.SafeRelease)
	if reason == "" && c.RuntimeFingerprint == "" {
		reason = "expansion_runtime_unattested"
	}
	if reason == "" {
		deployment := ""
		for _, b := range r.Apply.Builds {
			if b.AppID == c.AppID {
				deployment = b.DeploymentID
			}
		}
		snapshot, readErr := runtimeAppValuesDB(ctx, s.pool, account, c.AppID, deployment)
		if readErr != nil {
			reason = "runtime_inputs_changed"
		} else {
			fence, fenceErr := NewRuntimeAppConfigFence(snapshot)
			if fenceErr != nil || fence.Fingerprint != c.RuntimeFingerprint {
				reason = "runtime_inputs_changed"
			}
		}
	}
	if reason == "" {
		reason, err = q.MigrationContractBlocker(ctx, s.pool, migrationContractBlockerParams(c.ProjectID, attempt, r.Apply.SafeRelease))
		if err != nil {
			return nil, err
		}
	}
	if reason != "" {
		return &api.ProjectMigrationContract{Status: "blocked", Reason: reason}, nil
	}
	return &api.ProjectMigrationContract{Status: "ready", Reason: "fresh_health_check_required"}, nil
}
func migrationContractBlockerParams(project, attempt string, safe *api.ProjectSafeRelease) sqlc.MigrationContractBlockerParams {
	return sqlc.MigrationContractBlockerParams{ProjectID: project, AttemptID: attempt, ReleaseID: safe.ReleaseID, ConfigVersion: safe.Configuration.TargetVersion, ConfigHash: safe.Configuration.TargetHash}
}
func (s *PgStore) AdmitProjectMigrationContract(ctx context.Context, account, attempt string, req api.ProjectReleaseControlRequest, evidence ProjectMigrationContractEvidence) (*api.ProjectMigrationContract, error) {
	if !validContractRequest(req) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return nil, mapErr(err)
	}
	row, err := q.ReadMigrationContract(ctx, tx, sqlc.ReadMigrationContractParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return nil, mapErr(err)
	}
	c := migrationContractFromRow(row)
	if c.TaskID != "" {
		if c.RequestID != req.RequestID || c.Generation != req.ExpectedGeneration {
			return nil, ErrConflict
		}
		return migrationContractTaskStatusDB(ctx, tx, c)
	}
	var r api.ProjectSubmissionReceipt
	if err = json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	safe := r.Apply.SafeRelease
	if contractPlanReason(safe) != "" || safe.Verification.Generation != req.ExpectedGeneration || !validContractEvidence(safe, evidence) || safe.ReleaseCommand.ContractCommandHash != c.CommandHash {
		return nil, ErrConflict
	}
	if err = lockReleaseConfigurationEnvironmentDB(ctx, tx, account, c.ProjectID); err != nil {
		return nil, err
	}
	if err = q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: c.ProjectID, AccountID: account}); err != nil {
		return nil, err
	}
	if err = q.LockSafeProjectReleaseApps(ctx, tx, c.ProjectID); err != nil {
		return nil, err
	}
	if _, err = guardReleaseConfigurationDB(ctx, tx, account, attempt, c.ProjectID, safe, true); err != nil {
		return nil, err
	}
	reason, err := q.MigrationContractBlocker(ctx, tx, migrationContractBlockerParams(c.ProjectID, attempt, safe))
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, ErrConflict
	}
	retired, err := q.MigrationRetiredCandidates(ctx, tx, sqlc.MigrationRetiredCandidatesParams{ProjectID: c.ProjectID, ReleaseID: safe.ReleaseID})
	if err != nil {
		return nil, err
	}
	deployment := ""
	for _, build := range r.Apply.Builds {
		if build.AppID == c.AppID {
			if deployment != "" {
				return nil, ErrConflict
			}
			deployment = build.DeploymentID
		}
	}
	snapshot, err := runtimeAppValuesDB(ctx, tx, account, c.AppID, deployment)
	if err != nil {
		return nil, err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || c.RuntimeFingerprint == "" || fence.Fingerprint != c.RuntimeFingerprint || evidence.RuntimeFingerprint != c.RuntimeFingerprint || c.ExpansionTaskID != safe.ReleaseCommand.TaskID {
		return nil, ErrConflict
	}
	startDeadline := time.Now().UTC().Add(api.ProjectMigrationContractStartTimeout)
	params, err := resolveCreateAppTask(CreateAppTaskParams{AccountID: account, AppID: c.AppID, DeploymentID: deployment, Kind: AppTaskKindManual, Command: c.Command, CommandShell: true, StartDeadlineAt: &startDeadline})
	if err != nil {
		return nil, err
	}
	taskRow, err := q.CreateMigrationContractTask(ctx, tx, sqlc.CreateMigrationContractTaskParams{ID: uuid.NewString(), Command: params.Command, Timeout: int32(params.TimeoutSeconds), MaxOutput: int32(params.MaxOutputBytes), Backoff: int32(params.RetryBackoffSeconds), StartDeadline: pgtype.Timestamptz{Time: startDeadline, Valid: true}, DeploymentID: deployment, AppID: c.AppID, AccountID: account})
	if err != nil {
		return nil, mapErr(err)
	}
	task, err := appTaskFromSQLC(taskRow)
	if err != nil {
		return nil, err
	}
	n, err := q.AdmitMigrationContract(ctx, tx, sqlc.AdmitMigrationContractParams{AccountID: account, AttemptID: attempt, TaskID: task.ID, RequestID: req.RequestID, Generation: int64(req.ExpectedGeneration), ReleaseID: safe.ReleaseID, ConfigVersion: safe.Configuration.TargetVersion, ConfigHash: safe.Configuration.TargetHash, RetiredIds: retired})
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	c.TaskID, c.RequestID, c.Generation, c.RetiredIDs = task.ID, req.RequestID, req.ExpectedGeneration, retired
	return contractTaskResult(c, task, true, true), nil
}
