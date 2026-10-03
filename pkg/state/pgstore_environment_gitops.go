package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsStore = (*PgStore)(nil)

func gitOpsTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func environmentGitSourceFromSQL(row sqlc.EnvironmentGitSource, environment string) EnvironmentGitSource {
	out := EnvironmentGitSource{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID),
		EnvironmentID: pgUUIDString(row.EnvironmentID), EnvironmentSlug: environment,
		Spec: EnvironmentGitSourceSpec{
			RepositoryID: row.RepositoryID, InstallationID: row.InstallationID,
			Repository: row.Repository, Ref: row.SourceRef, ManifestPath: row.ManifestPath,
			Mode: row.Mode, ApprovalPolicy: row.ApprovalPolicy, Prune: row.Prune,
		},
		Suspended: row.Suspended, Generation: row.Generation, IntentVersion: row.IntentVersion,
		ApprovedRevisionID: pgUUIDString(row.ApprovedRevisionID), AppliedRevisionID: pgUUIDString(row.AppliedRevisionID),
		SourceErrorCode: row.SourceErrorCode, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		SourceCommitSHA: row.SourceCommitSha, SourceDefinitionDigest: row.SourceDefinitionDigest,
	}
	if row.SourceCheckedAt.Valid {
		checked := row.SourceCheckedAt.Time
		out.SourceCheckedAt = &checked
	}
	if row.SourceVerifiedAt.Valid {
		verified := row.SourceVerifiedAt.Time
		out.SourceVerifiedAt = &verified
	}
	return out
}

func environmentRevisionFromSQL(row sqlc.EnvironmentDesiredRevision) EnvironmentDesiredRevision {
	return EnvironmentDesiredRevision{
		ID: pgUUIDString(row.ID), SourceID: pgUUIDString(row.SourceID), CommitSHA: row.CommitSha,
		Digest: row.DefinitionDigest, Definition: append(json.RawMessage(nil), row.Definition...),
		ApprovedBy: row.ApprovedBy, ApprovedAt: row.ApprovedAt.Time,
	}
}

func environmentRunFromSQL(row sqlc.EnvironmentGitopsRun) EnvironmentGitOpsRun {
	out := EnvironmentGitOpsRun{
		ID: pgUUIDString(row.ID), SourceID: pgUUIDString(row.SourceID), RevisionID: pgUUIDString(row.RevisionID),
		Generation: row.Generation, Status: row.Status, Plan: append(json.RawMessage(nil), row.Plan...),
		Steps: append(json.RawMessage(nil), row.Steps...), ErrorCode: row.ErrorCode, StartedAt: row.StartedAt.Time,
	}
	if row.CompletedAt.Valid {
		completed := row.CompletedAt.Time
		out.CompletedAt = &completed
	}
	return out
}

func (s *PgStore) CreateEnvironmentGitSource(ctx context.Context, accountID, projectID, environment string, spec EnvironmentGitSourceSpec) (EnvironmentGitSource, error) {
	if err := spec.Validate(); err != nil {
		return EnvironmentGitSource{}, fmt.Errorf("git source: %w: %w", ErrInvalidArgument, err)
	}
	row, err := sqlc.New().CreateEnvironmentGitSource(ctx, s.pool, sqlc.CreateEnvironmentGitSourceParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment,
		RepositoryID: spec.RepositoryID, InstallationID: spec.InstallationID, Repository: spec.Repository,
		SourceRef: spec.Ref, ManifestPath: spec.ManifestPath, Mode: spec.Mode, ApprovalPolicy: spec.ApprovalPolicy, Prune: spec.Prune,
	})
	if err != nil {
		return EnvironmentGitSource{}, mapErr(err)
	}
	return environmentGitSourceFromSQL(row, environment), nil
}

func (s *PgStore) EnvironmentGitSource(ctx context.Context, accountID, projectID, environment string) (EnvironmentGitSource, error) {
	row, err := sqlc.New().GetEnvironmentGitSource(ctx, s.pool, sqlc.GetEnvironmentGitSourceParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment,
	})
	if err != nil {
		return EnvironmentGitSource{}, mapErr(err)
	}
	return environmentGitSourceFromSQL(row, environment), nil
}

func validateEnvironmentApproval(input ApproveEnvironmentRevision) (environmentsync.DesiredState, error) {
	if !environmentCommitRE.MatchString(input.CommitSHA) || input.ApprovedBy == "" || input.ExpectedGeneration < 0 {
		return environmentsync.DesiredState{}, ErrInvalidArgument
	}
	desired, err := environmentsync.Compile(input.Desired.Definition)
	if err != nil || desired.Digest != input.Desired.Digest {
		return environmentsync.DesiredState{}, ErrInvalidArgument
	}
	return desired, nil
}

func (s *PgStore) ApproveEnvironmentDesiredRevision(ctx context.Context, input ApproveEnvironmentRevision) (EnvironmentGitSource, EnvironmentDesiredRevision, error) {
	desired, err := validateEnvironmentApproval(input)
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	source, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{
		AccountID: mustPgUUID(input.AccountID), SourceID: mustPgUUID(input.SourceID),
	})
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, mapErr(err)
	}
	if source.Generation != input.ExpectedGeneration || source.Suspended {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrConflict
	}
	if source.ApprovalPolicy != "manual" {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrInvalidArgument
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, source.ID)
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, mapErr(err)
	}
	if desired.Definition.Project != scope.ProjectSlug || desired.Definition.Environment != scope.EnvironmentSlug {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, ErrInvalidArgument
	}
	definition, _ := json.Marshal(desired.Definition)
	revision, err := q.InsertEnvironmentDesiredRevision(ctx, tx, sqlc.InsertEnvironmentDesiredRevisionParams{
		SourceID: source.ID, CommitSha: input.CommitSHA, DefinitionDigest: desired.Digest,
		Definition: definition, ApprovedBy: input.ApprovedBy,
	})
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, mapErr(err)
	}
	// A retry of the current approval does not change the generation or revoke
	// its running lease. Explicit rollback to a prior revision does.
	if source.ApprovedRevisionID != revision.ID {
		source, err = q.SetEnvironmentApprovedRevision(ctx, tx, sqlc.SetEnvironmentApprovedRevisionParams{
			RevisionID: revision.ID, SourceID: source.ID, ExpectedGeneration: input.ExpectedGeneration,
		})
		if err != nil {
			return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, mapErr(err)
		}
		if err := q.EnqueueEnvironmentGitOps(ctx, tx, sqlc.EnqueueEnvironmentGitOpsParams{
			SourceID: source.ID, Generation: source.Generation, NextAttemptAt: gitOpsTime(time.Now()),
		}); err != nil {
			return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, mapErr(err)
	}
	return environmentGitSourceFromSQL(source, scope.EnvironmentSlug), environmentRevisionFromSQL(revision), nil
}

func (s *PgStore) ClaimEnvironmentGitOps(ctx context.Context, token string, now time.Time, duration time.Duration) (EnvironmentGitOpsLease, error) {
	if token == "" || duration <= 0 || now.IsZero() {
		return EnvironmentGitOpsLease{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentGitOpsLease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := q.ClaimEnvironmentGitOpsJob(ctx, tx, sqlc.ClaimEnvironmentGitOpsJobParams{
		LeaseToken: token, LeaseUntil: gitOpsTime(now.Add(duration)), NowAt: gitOpsTime(now),
	})
	if err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	source, err := q.GetEnvironmentGitSourceByID(ctx, tx, job.SourceID)
	if err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	revision, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{
		SourceID: source.ID, RevisionID: source.ApprovedRevisionID,
	})
	if err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, source.ID)
	if err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	if err := q.SupersedeEnvironmentGitOpsRuns(ctx, tx, sqlc.SupersedeEnvironmentGitOpsRunsParams{SourceID: source.ID, NowAt: gitOpsTime(now)}); err != nil {
		return EnvironmentGitOpsLease{}, err
	}
	run, err := q.InsertEnvironmentGitOpsRun(ctx, tx, sqlc.InsertEnvironmentGitOpsRunParams{
		SourceID: source.ID, RevisionID: revision.ID, Generation: source.Generation, LeaseToken: token, NowAt: gitOpsTime(now),
	})
	if err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentGitOpsLease{}, mapErr(err)
	}
	return EnvironmentGitOpsLease{
		RunID: pgUUIDString(run.ID), Source: environmentGitSourceFromSQL(source, scope.EnvironmentSlug),
		Revision: environmentRevisionFromSQL(revision), LeaseToken: token, LeaseUntil: job.LeaseUntil.Time, AttemptCount: int(job.AttemptCount),
	}, nil
}

// lockEnvironmentGitOps always locks the source before its job. Approval and
// claiming use the same order, avoiding the source/job lock inversion.
func lockEnvironmentGitOps(ctx context.Context, tx pgx.Tx, lease EnvironmentGitOpsLease, now time.Time) error {
	q := sqlc.New()
	source, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{
		AccountID: mustPgUUID(lease.Source.AccountID), SourceID: mustPgUUID(lease.Source.ID),
	})
	if err != nil {
		return mapErr(err)
	}
	if source.Generation != lease.Source.Generation || pgUUIDString(source.ApprovedRevisionID) != lease.Revision.ID || source.Suspended {
		return ErrConflict
	}
	_, err = q.LockEnvironmentGitOpsLease(ctx, tx, sqlc.LockEnvironmentGitOpsLeaseParams{
		SourceID: source.ID, LeaseToken: lease.LeaseToken, Generation: source.Generation, NowAt: gitOpsTime(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) RenewEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, now time.Time, duration time.Duration) error {
	if duration <= 0 || now.IsZero() {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEnvironmentGitOps(ctx, tx, lease, now); err != nil {
		return err
	}
	_, err = sqlc.New().RenewEnvironmentGitOpsLease(ctx, tx, sqlc.RenewEnvironmentGitOpsLeaseParams{
		SourceID: mustPgUUID(lease.Source.ID), LeaseToken: lease.LeaseToken, LeaseUntil: gitOpsTime(now.Add(duration)),
	})
	if err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func validateGitOpsRunFinish(lease EnvironmentGitOpsLease, status string, plan, steps json.RawMessage, now, next time.Time) error {
	switch status {
	case "drifted", "blocked", "partial", "overridden", "converged", "failed":
	default:
		return ErrInvalidArgument
	}
	var object map[string]json.RawMessage
	var array []json.RawMessage
	if json.Unmarshal(plan, &object) != nil || object == nil || json.Unmarshal(steps, &array) != nil || array == nil || now.IsZero() || next.Before(now) {
		return ErrInvalidArgument
	}
	if status == "converged" {
		var verified environmentsync.Plan
		if json.Unmarshal(plan, &verified) != nil || !verified.CanApply() || verified.HasDrift() ||
			verified.Manager != lease.Source.ID || verified.Revision != lease.Revision.ID ||
			verified.Generation != lease.Source.Generation || verified.DesiredDigest != lease.Revision.Digest || len(verified.Hash) != 64 {
			return ErrInvalidArgument
		}
	}
	return nil
}

func (s *PgStore) FinishEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, status string, plan, steps json.RawMessage, errorCode string, now, next time.Time) error {
	if err := validateGitOpsRunFinish(lease, status, plan, steps, now, next); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEnvironmentGitOps(ctx, tx, lease, now); err != nil {
		return err
	}
	q := sqlc.New()
	if status == "converged" {
		var verified environmentsync.Plan
		_ = json.Unmarshal(plan, &verified) // validated before opening the transaction
		source, err := q.GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(lease.Source.ID))
		if err != nil {
			return mapErr(err)
		}
		if source.IntentVersion != verified.ObservedVersion {
			return ErrConflict
		}
		pending, err := q.HasPendingEnvironmentGitOpsEffects(ctx, tx, source.ID)
		if err != nil {
			return mapErr(err)
		}
		if pending {
			return ErrConflict
		}
		pendingRuntime, err := q.HasPendingEnvironmentGitOpsRuntime(ctx, tx, source.ID)
		if err != nil {
			return mapErr(err)
		}
		runtimeDrift, err := q.HasEnvironmentGitOpsRuntimeDrift(ctx, tx, source.ID)
		if err != nil {
			return mapErr(err)
		}
		if pendingRuntime || runtimeDrift {
			return ErrConflict
		}
	}
	count, err := q.FinishEnvironmentGitOpsRun(ctx, tx, sqlc.FinishEnvironmentGitOpsRunParams{
		RunID: mustPgUUID(lease.RunID), SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation,
		LeaseToken: lease.LeaseToken, Status: status, Plan: plan, Steps: steps, ErrorCode: errorCode, NowAt: gitOpsTime(now),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	if status == "converged" {
		if _, err := q.SetEnvironmentAppliedRevision(ctx, tx, sqlc.SetEnvironmentAppliedRevisionParams{SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation}); err != nil {
			return mapErr(err)
		}
	}
	if _, err := q.ReleaseEnvironmentGitOpsLease(ctx, tx, sqlc.ReleaseEnvironmentGitOpsLeaseParams{SourceID: mustPgUUID(lease.Source.ID), LeaseToken: lease.LeaseToken, NextAttemptAt: gitOpsTime(next)}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListEnvironmentGitOpsRuns(ctx context.Context, accountID, sourceID string, limit int) ([]EnvironmentGitOpsRun, error) {
	if limit < 1 || int64(limit) > int64(^uint32(0)>>1) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListEnvironmentGitOpsRuns(ctx, s.pool, sqlc.ListEnvironmentGitOpsRunsParams{
		AccountID: mustPgUUID(accountID), SourceID: mustPgUUID(sourceID), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentGitOpsRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, environmentRunFromSQL(row))
	}
	return out, nil
}
