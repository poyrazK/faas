package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeUpgradeOperationStore = (*PgStore)(nil)

func (s *PgStore) RegisterRuntimeUpgradeOperation(ctx context.Context, r RuntimeUpgradeOperationRequest) (RuntimeUpgradeOperation, error) {
	if err := r.validate(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("begin runtime upgrade operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRuntimeUpgradeCutoverDB(ctx, tx, r.cutover("")); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("lock runtime upgrade preparation: %w", runtimeUpgradeBaselineError(err))
	}
	op, err := registerRuntimeUpgradeOperationDB(ctx, tx, r)
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("register runtime upgrade operation: %w", runtimeUpgradeBaselineError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("commit runtime upgrade operation: %w", runtimeUpgradeBaselineError(err))
	}
	return op, nil
}

func registerRuntimeUpgradeOperationDB(ctx context.Context, tx pgx.Tx, r RuntimeUpgradeOperationRequest) (RuntimeUpgradeOperation, error) {
	q := sqlc.New()
	old, err := q.GetRuntimeUpgradeOperation(ctx, tx, mustPgUUID(r.ID))
	if err == nil {
		op := runtimeUpgradeOperationFromRow(old)
		if op.RuntimeUpgradeOperationRequest != r {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		return op, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, err
	}
	if _, err := q.BuildByID(ctx, tx, mustPgUUID(r.ID)); err == nil {
		return RuntimeUpgradeOperation{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, err
	}
	candidate, err := q.ReadRuntimeUpgradeOperationCandidate(ctx, tx, mustPgUUID(r.DeploymentID))
	if err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	if candidate.Status != string(DeployPending) || candidate.HasBuild || candidate.RootfsKey != "" || candidate.RootfsPath != "" || candidate.ImageDigest != "" || !runtimeUpgradeOperationCandidateRowMatches(candidate, r) {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if err := runtimeUpgradeOperationQualificationDB(ctx, tx, r); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	if _, err := q.PinDeploymentRuntimeUpgradeTarget(ctx, tx, sqlc.PinDeploymentRuntimeUpgradeTargetParams{DeploymentID: mustPgUUID(r.DeploymentID), ReleaseID: r.TargetReleaseID, SourceSha256: r.SourceSHA256, SourceFieldLimit: api.RuntimeUpgradeSourceFieldMaxBytes}); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	baseline, err := runtimeUpgradeBaselineDB(ctx, tx, r.DeploymentID, r.ServingDeploymentID)
	if err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	if _, err := q.InsertDeploymentRuntimeUpgradeBaseline(ctx, tx, sqlc.InsertDeploymentRuntimeUpgradeBaselineParams{
		DeploymentID: mustPgUUID(r.DeploymentID), ServingDeploymentID: mustPgUUID(r.ServingDeploymentID), ServingRootfsKey: baseline.ServingRootfsKey,
		ServingRuntimeReleaseID: baseline.ServingRuntimeReleaseID, TargetReleaseID: baseline.TargetReleaseID,
		ConfigurationFingerprint: baseline.ConfigurationFingerprint, SecretFingerprint: baseline.SecretFingerprint, InputFingerprint: baseline.InputFingerprint, InputSecretFingerprint: baseline.InputSecretFingerprint,
	}); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	row, err := q.InsertRuntimeUpgradeOperation(ctx, tx, sqlc.InsertRuntimeUpgradeOperationParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID), DeploymentID: mustPgUUID(r.DeploymentID), ServingDeploymentID: mustPgUUID(r.ServingDeploymentID), TargetReleaseID: r.TargetReleaseID, SourceSha256: r.SourceSHA256, QualificationReportSha256: r.QualificationReportSHA256, DeadlineSeconds: int32(api.RuntimeUpgradeOperationMaxAge / time.Second)})
	return runtimeUpgradeOperationFromRow(row), mapErr(err)
}

func runtimeUpgradeOperationCandidateRowMatches(d sqlc.ReadRuntimeUpgradeOperationCandidateRow, r RuntimeUpgradeOperationRequest) bool {
	return !d.UnsupportedMode && d.SourcePath != "" && d.SourceSha256 == r.SourceSHA256 && !d.DeletedAt.Valid && d.TrafficPercent == 0 && d.TrafficPercentExplicit &&
		d.CanaryTotalSteps == 0 && NormalizeRolloutState(d.RolloutState) != "rolling_out" && len(d.EnvironmentWorkloadRuntime) == 0
}

func runtimeUpgradeOperationQualificationDB(ctx context.Context, tx pgx.Tx, r RuntimeUpgradeOperationRequest) error {
	q := sqlc.New()
	target, err := q.GetRuntimeRelease(ctx, tx, r.TargetReleaseID)
	if err != nil {
		return err
	}
	qualification, err := q.LockRuntimeReleaseQualification(ctx, tx, r.TargetReleaseID)
	if err != nil {
		return err
	}
	if qualification.ReportSha256 != r.QualificationReportSHA256 || !validRuntimeReleaseQualification(runtimeReleaseFromRow(target), runtimeQualificationFromRow(qualification), time.Now().UTC()) {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) RuntimeUpgradeOperation(ctx context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeOperation{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetRuntimeUpgradeOperation(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return RuntimeUpgradeOperation{}, mapErr(err)
	}
	op := runtimeUpgradeOperationFromRow(row)
	if op.AccountID != accountID {
		return RuntimeUpgradeOperation{}, ErrNotFound
	}
	return op, nil
}

func (s *PgStore) ClaimRuntimeUpgradeOperation(ctx context.Context) (RuntimeUpgradeOperationClaim, error) {
	row, err := sqlc.New().ClaimRuntimeUpgradeOperation(ctx, s.pool, int32(api.RuntimeUpgradeOperationLease/time.Second))
	if err != nil {
		return RuntimeUpgradeOperationClaim{}, mapErr(err)
	}
	return RuntimeUpgradeOperationClaim{ID: pgUUIDString(row.ID), LeaseToken: pgUUIDString(row.LeaseToken)}, nil
}

func (s *PgStore) AdvanceRuntimeUpgradeOperation(ctx context.Context, c RuntimeUpgradeOperationClaim) (RuntimeUpgradeOperation, error) {
	if err := c.validate(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("begin runtime upgrade advance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockRuntimeUpgradeOperation(ctx, tx, sqlc.LockRuntimeUpgradeOperationParams{ID: mustPgUUID(c.ID), LeaseToken: mustPgUUID(c.LeaseToken)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("lock runtime upgrade lease: %w", err)
	}
	op := runtimeUpgradeOperationFromRow(row)
	phase, blocker, wake, err := advanceRuntimeUpgradeOperationDB(ctx, tx, op)
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("advance runtime upgrade: %w", err)
	}
	wakeID := pgtype.UUID{}
	if wake != "" {
		wakeID = mustPgUUID(wake)
	}
	row, err = q.AdvanceRuntimeUpgradeOperation(ctx, tx, sqlc.AdvanceRuntimeUpgradeOperationParams{ID: mustPgUUID(c.ID), LeaseToken: mustPgUUID(c.LeaseToken), Phase: string(phase), Blocker: blocker, WakeID: wakeID, IntervalSeconds: int32(api.RuntimeUpgradeOperationInterval / time.Second)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeOperation{}, ErrConflict
	} // expired during lock wait: roll back every effect
	if err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("checkpoint runtime upgrade: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeUpgradeOperation{}, fmt.Errorf("commit runtime upgrade advance: %w", runtimeUpgradeBaselineError(err))
	}
	return runtimeUpgradeOperationFromRow(row), nil
}

func advanceRuntimeUpgradeOperationDB(ctx context.Context, tx pgx.Tx, op RuntimeUpgradeOperation) (RuntimeUpgradeOperationPhase, string, string, error) {
	q, r := sqlc.New(), op.RuntimeUpgradeOperationRequest
	old, err := q.GetDeploymentRuntimeUpgradeCutover(ctx, tx, mustPgUUID(r.DeploymentID))
	if err == nil {
		cutover := runtimeUpgradeCutoverFromRow(old)
		if !r.cutover(cutover.WakeID).matches(cutover) {
			return blockedRuntimeUpgradeOperation("intent_changed")
		}
		return RuntimeUpgradeComplete, "", cutover.WakeID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", err
	}
	if err := lockRuntimeUpgradeCutoverDB(ctx, tx, r.cutover("")); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return blockedRuntimeUpgradeOperation("candidate_changed")
		}
		return "", "", "", err
	}
	if !op.DeadlineAt.After(time.Now().UTC()) {
		return blockedRuntimeUpgradeOperation("deadline_exceeded")
	}
	d, err := q.ReadRuntimeUpgradeOperationCandidate(ctx, tx, mustPgUUID(r.DeploymentID))
	if err != nil {
		return "", "", "", err
	}
	if !runtimeUpgradeOperationCandidateRowMatches(d, r) || runtimeUpgradeOperationTerminal(Deployment{Status: DeploymentStatus(d.Status)}) {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	baseline, err := q.GetDeploymentRuntimeUpgradeBaseline(ctx, tx, mustPgUUID(r.DeploymentID))
	if err != nil {
		return "", "", "", err
	}
	current, err := runtimeUpgradeBaselineDB(ctx, tx, r.DeploymentID, r.ServingDeploymentID)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return blockedRuntimeUpgradeOperation("baseline_changed")
	}
	if err != nil {
		return "", "", "", err
	}
	if baseline.ServingDeploymentID != mustPgUUID(r.ServingDeploymentID) || baseline.TargetReleaseID != r.TargetReleaseID || !sameRuntimeUpgradeBaseline(runtimeUpgradeBaselineFromRow(baseline), current) {
		return blockedRuntimeUpgradeOperation("baseline_changed")
	}
	if err := runtimeUpgradeOperationQualificationDB(ctx, tx, r); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, pgx.ErrNoRows) {
			return blockedRuntimeUpgradeOperation("qualification_changed")
		}
		return "", "", "", err
	}
	if !op.DeadlineAt.After(time.Now().UTC()) {
		return blockedRuntimeUpgradeOperation("deadline_exceeded")
	}
	if op.Phase == RuntimeUpgradePrepared {
		if d.Status != string(DeployPending) || d.HasBuild || d.RootfsKey != "" || d.RootfsPath != "" || d.ImageDigest != "" {
			return blockedRuntimeUpgradeOperation("candidate_changed")
		}
		if _, err := q.QueueRuntimeUpgradeOperationBuild(ctx, tx, sqlc.QueueRuntimeUpgradeOperationBuildParams{BuildID: mustPgUUID(r.ID), DeploymentID: mustPgUUID(r.DeploymentID)}); err != nil {
			return "", "", "", err
		}
		count, err := q.PublishRuntimeUpgradeOperationBuild(ctx, tx, sqlc.PublishRuntimeUpgradeOperationBuildParams{BuildID: mustPgUUID(r.ID), DeploymentID: mustPgUUID(r.DeploymentID)})
		if err != nil {
			return "", "", "", err
		}
		if count != 1 {
			return "", "", "", ErrConflict
		}
		if err := q.NotifyRuntimeUpgradeOperationBuild(ctx, tx, sqlc.NotifyRuntimeUpgradeOperationBuildParams{BuildID: r.ID, DeploymentID: mustPgUUID(r.DeploymentID)}); err != nil {
			return "", "", "", err
		}
		return RuntimeUpgradeWaiting, "", "", nil
	}
	if d.BuildID != r.ID {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	if (d.BuildStatus != string(BuildQueued) && d.BuildStatus != string(BuildRunning) && d.BuildStatus != string(BuildSucceeded)) || (d.Status == string(DeployLive) && d.BuildStatus != string(BuildSucceeded)) {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	if d.Status != string(DeployLive) {
		return RuntimeUpgradeWaiting, "", "", nil
	}
	acceptance, err := q.GetDeploymentRuntimeUpgradeAcceptance(ctx, tx, mustPgUUID(r.DeploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeUpgradeWaiting, "", "", nil
	}
	if err != nil {
		return "", "", "", err
	}
	cutover, err := runtimeUpgradeCutoverDB(ctx, tx, r.cutover(pgUUIDString(acceptance.WakeID)))
	if errors.Is(err, ErrConflict) {
		return blockedRuntimeUpgradeOperation("readiness_changed")
	}
	if err != nil {
		return "", "", "", err
	}
	return RuntimeUpgradeComplete, "", cutover.WakeID, nil
}

func runtimeUpgradeOperationFromRow(r sqlc.RuntimeUpgradeOperation) RuntimeUpgradeOperation {
	return RuntimeUpgradeOperation{RuntimeUpgradeOperationRequest: RuntimeUpgradeOperationRequest{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), DeploymentID: pgUUIDString(r.DeploymentID), ServingDeploymentID: pgUUIDString(r.ServingDeploymentID), TargetReleaseID: r.TargetReleaseID, SourceSHA256: r.SourceSha256, QualificationReportSHA256: r.QualificationReportSha256},
		Phase: RuntimeUpgradeOperationPhase(r.Phase), Blocker: r.Blocker, WakeID: pgUUIDString(r.WakeID), LeaseToken: pgUUIDString(r.LeaseToken), CreatedAt: r.CreatedAt.Time.UTC(), DeadlineAt: r.DeadlineAt.Time.UTC(), NextAttemptAt: r.NextAttemptAt.Time.UTC(), LeaseUntil: r.LeaseUntil.Time.UTC(), FinishedAt: r.FinishedAt.Time.UTC()}
}
