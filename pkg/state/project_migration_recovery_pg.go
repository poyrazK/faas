package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recoveryFromRow(r sqlc.ReadMigrationRecoveryRow) *migrationRecoveryRecord {
	at := r.ResolvedAt.Time
	return &migrationRecoveryRecord{RequestHash: r.RequestHash, Receipt: api.ProjectMigrationRecovery{Status: "resolved", RequestID: r.RequestID, TaskID: r.TaskID, ExpectedGeneration: uint64(r.Generation), ReplacementAttemptID: r.ReplacementAttemptID, ReplacementReleaseID: r.ReplacementReleaseID, EvidenceHash: r.EvidenceHash, OperatorAccountID: r.AccountID, ResolvedAt: &at}}
}
func (s *PgStore) ResolveProjectMigrationContract(ctx context.Context, account, attempt string, req api.ProjectMigrationRecoveryRequest, e ProjectMigrationRecoveryEvidence) (*api.ProjectMigrationRecovery, error) {
	if !req.Valid() || req.ReplacementAttemptID == attempt {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: account, AttemptID: attempt}); err != nil {
		return nil, mapErr(err)
	}
	row, err := q.ReadMigrationContract(ctx, tx, sqlc.ReadMigrationContractParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return nil, mapErr(err)
	}
	c := migrationContractFromRow(row)
	previous, err := q.ReadMigrationRecovery(ctx, tx, sqlc.ReadMigrationRecoveryParams{AccountID: account, AttemptID: attempt})
	if err == nil {
		record := recoveryFromRow(previous)
		if record.RequestHash != req.Fingerprint() {
			return nil, ErrConflict
		}
		return copyMigrationRecovery(record.Receipt), nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return nil, err
	}
	taskRow, err := q.ReadMigrationContractTask(ctx, tx, c.TaskID)
	if err != nil {
		return nil, mapErr(err)
	}
	task, err := appTaskFromSQLC(taskRow)
	if err != nil {
		return nil, err
	}
	if !recoveryTaskEligible(c, task, true, req) {
		return nil, ErrConflict
	}
	replacement, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: account, AttemptID: req.ReplacementAttemptID})
	if err != nil {
		return nil, mapErr(err)
	}
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(replacement.Payload, &r) != nil || !recoveryReplacementEligible(r, c.ProjectID, c.ReleaseID) || !recoveryEvidenceEligible(r, e) {
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
	safe := r.Apply.SafeRelease
	reason, err := q.MigrationRecoveryBlocker(ctx, tx, sqlc.MigrationRecoveryBlockerParams{ProjectID: c.ProjectID, ReleaseID: safe.ReleaseID, ConfigVersion: safe.Configuration.TargetVersion, ConfigHash: safe.Configuration.TargetHash, AttemptID: req.ReplacementAttemptID, IgnoredTaskID: c.TaskID})
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, &MigrationRecoveryBlockedError{Reason: reason}
	}
	graph, err := readProjectReleaseSetTx(ctx, tx, account, c.ProjectID, "production", safe.ReleaseID)
	if err != nil {
		return nil, err
	}
	if len(graph.Members) != len(r.Apply.Builds) || len(e.RuntimeFingerprints) != len(graph.Members) {
		return nil, ErrConflict
	}
	for _, member := range graph.Members {
		matches := 0
		for _, build := range r.Apply.Builds {
			if build.AppID == member.AppID && build.DeploymentID == member.DeploymentID {
				matches++
			}
		}
		snapshot, err := runtimeAppValuesDB(ctx, tx, account, member.AppID, member.DeploymentID)
		if err != nil || matches != 1 {
			return nil, ErrConflict
		}
		fence, err := NewRuntimeAppConfigFence(snapshot)
		if err != nil || fence.Fingerprint != e.RuntimeFingerprints[member.AppID] {
			return nil, ErrConflict
		}
	}
	retired, err := q.MigrationRetiredCandidates(ctx, tx, sqlc.MigrationRetiredCandidatesParams{ProjectID: c.ProjectID, ReleaseID: safe.ReleaseID})
	if err != nil {
		return nil, err
	}
	count, err := q.RetireMigrationRecoveryDeployments(ctx, tx, sqlc.RetireMigrationRecoveryDeploymentsParams{AccountID: account, AttemptID: attempt, TaskID: c.TaskID, Generation: int64(c.Generation), RetiredIds: recoveryRetiredUUIDs(retired)})
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrConflict
	}
	receipt := recoveryReceipt(account, req, safe.ReleaseID)
	fingerprints, _ := json.Marshal(e.RuntimeFingerprints)
	err = q.InsertMigrationRecovery(ctx, tx, sqlc.InsertMigrationRecoveryParams{AccountID: account, AttemptID: attempt, ProjectID: c.ProjectID, RequestID: req.RequestID, RequestHash: req.Fingerprint(), TaskID: c.TaskID, Generation: int64(c.Generation), ReplacementAttemptID: req.ReplacementAttemptID, ReplacementReleaseID: safe.ReleaseID, ConfigVersion: safe.Configuration.TargetVersion, ConfigHash: safe.Configuration.TargetHash, RuntimeFingerprints: fingerprints, EvidenceHash: req.EvidenceHash, ResolvedAt: pgtype.Timestamptz{Time: *receipt.ResolvedAt, Valid: true}})
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func recoveryRetiredUUIDs(ids []string) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		out = append(out, mustPgUUID(id))
	}
	return out
}
