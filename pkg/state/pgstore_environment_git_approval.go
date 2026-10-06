package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitApprovalStore = (*PgStore)(nil)

func environmentGitApprovalFromSQL(row sqlc.EnvironmentGitRevisionApproval) (EnvironmentGitRevisionApproval, error) {
	record := EnvironmentGitRevisionApproval{ID: pgUUIDString(row.ID), SourceID: pgUUIDString(row.SourceID), RevisionID: pgUUIDString(row.RevisionID),
		Generation: row.ApprovedGeneration, DefinitionDigest: row.DefinitionDigest, RecordedAt: row.RecordedAt.Time}
	if err := json.Unmarshal(row.Evidence, &record.Evidence); err != nil {
		return EnvironmentGitRevisionApproval{}, err
	}
	return record, nil
}

func (s *PgStore) EnvironmentGitRevisionApproval(ctx context.Context, accountID, sourceID, revisionID string) (EnvironmentGitRevisionApproval, error) {
	row, err := sqlc.New().GetEnvironmentGitRevisionApproval(ctx, s.pool, sqlc.GetEnvironmentGitRevisionApprovalParams{
		AccountID: mustPgUUID(accountID), SourceID: mustPgUUID(sourceID), RevisionID: mustPgUUID(revisionID)})
	if err != nil {
		return EnvironmentGitRevisionApproval{}, mapErr(err)
	}
	return environmentGitApprovalFromSQL(row)
}

// The caller holds the source row. Provenance, pointer, generation, candidate,
// poll lease completion and durable work all commit or roll back together.
func approveEnvironmentGitPoll(ctx context.Context, tx pgx.Tx, source sqlc.EnvironmentGitSource, lease EnvironmentGitSourcePollLease, result EnvironmentGitSourcePollResult) error {
	q := sqlc.New()
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, source.ID)
	if err != nil {
		return mapErr(err)
	}
	if result.Desired.Definition.Project != scope.ProjectSlug || result.Desired.Definition.Environment != scope.EnvironmentSlug {
		return ErrInvalidArgument
	}
	definition, _ := json.Marshal(result.Desired.Definition)
	revision, err := q.InsertEnvironmentDesiredRevision(ctx, tx, sqlc.InsertEnvironmentDesiredRevisionParams{SourceID: source.ID, CommitSha: result.CommitSHA,
		DefinitionDigest: result.Digest, Definition: definition, ApprovedBy: "github:protected_branch"})
	if err != nil {
		return mapErr(err)
	}
	changed := source.ApprovedRevisionID != revision.ID
	generation := source.Generation
	if changed {
		generation++
	}
	params := sqlc.GetEnvironmentGitRevisionApprovalParams{AccountID: source.AccountID, SourceID: source.ID, RevisionID: revision.ID}
	_, readErr := q.GetEnvironmentGitRevisionApproval(ctx, tx, params)
	if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
		return mapErr(readErr)
	}
	if !changed && readErr == nil {
		return nil
	}
	evidence, _ := json.Marshal(result.Approval)
	if err := q.InsertEnvironmentGitRevisionApproval(ctx, tx, sqlc.InsertEnvironmentGitRevisionApprovalParams{SourceID: source.ID, RevisionID: revision.ID,
		ApprovedGeneration: generation, DefinitionDigest: result.Digest, Evidence: evidence, PollLeaseToken: mustPgUUID(lease.LeaseToken)}); err != nil {
		return mapErr(err)
	}
	record, err := q.GetEnvironmentGitRevisionApproval(ctx, tx, params)
	if err != nil {
		return mapErr(err)
	}
	if changed {
		if err := q.SetEnvironmentGitApprovalContext(ctx, tx, pgUUIDString(record.ID)); err != nil {
			return err
		}
		source, err = q.SetEnvironmentApprovedRevision(ctx, tx, sqlc.SetEnvironmentApprovedRevisionParams{SourceID: source.ID, RevisionID: revision.ID, ExpectedGeneration: source.Generation})
		if err != nil {
			return mapErr(err)
		}
	}
	return q.EnqueueEnvironmentGitOps(ctx, tx, sqlc.EnqueueEnvironmentGitOpsParams{SourceID: source.ID, Generation: source.Generation, NextAttemptAt: gitOpsTime(time.Now())})
}
