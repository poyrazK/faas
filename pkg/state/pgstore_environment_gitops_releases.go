package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListEnvironmentGitReleases(ctx context.Context, source EnvironmentGitSource, request api.ListEnvironmentGitReleasesRequest, desired environmentsync.DesiredState) (api.ListEnvironmentGitReleasesResponse, error) {
	out, err := recoveryListing(source, request, desired, time.Now().UTC())
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, err = promotionSourceTx(ctx, tx, source)
	if err != nil {
		return out, err
	}
	out.ObservedAt = time.Now().UTC()
	rawPolicy, err := sqlc.New().ReadEnvironmentRecoveryPolicy(ctx, tx, mustPgUUID(source.ID))
	if err != nil {
		return out, mapErr(err)
	}
	if err := json.Unmarshal(rawPolicy, &out.RecoveryRetention); err != nil {
		return out, err
	}

	params := sqlc.ListEnvironmentGitRecoveryReleasesParams{AccountID: mustPgUUID(source.AccountID), ProjectID: mustPgUUID(source.ProjectID), Environment: source.EnvironmentSlug, SourceID: mustPgUUID(source.ID), EnvironmentID: mustPgUUID(source.EnvironmentID), PageLimit: api.EnvironmentGitRecoveryPageSize + 1}
	q := sqlc.New()
	if request.Before != "" {
		raw, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: params.AccountID, ProjectID: params.ProjectID, Environment: params.Environment, ReleaseID: mustPgUUID(request.Before)})
		if err != nil {
			return out, mapErr(err)
		}
		anchor, err := decodeProjectReleaseSet(raw)
		if err != nil {
			return out, err
		}
		graphs, err := q.EnvironmentWorkloadGraphsForRollback(ctx, tx, sqlc.EnvironmentWorkloadGraphsForRollbackParams{SourceID: params.SourceID, EnvironmentID: params.EnvironmentID, ReleaseID: mustPgUUID(anchor.ID)})
		if err != nil {
			return out, err
		}
		found := false
		for _, graph := range graphs {
			receipt, _, err := loadEnvironmentWorkloadServingReceipt(ctx, tx, graph.ID)
			if err != nil {
				return out, err
			}
			found = found || historicalServingAt(receipt) != nil
		}
		if !found {
			return out, ErrNotFound
		}
		params.BeforeAt, params.BeforeID = pgtype.Timestamptz{Time: anchor.CreatedAt, Valid: true}, mustPgUUID(anchor.ID)
	}
	ids, err := q.ListEnvironmentGitRecoveryReleases(ctx, tx, params)
	if err != nil {
		return out, err
	}
	if len(ids) > api.EnvironmentGitRecoveryPageSize {
		ids = ids[:api.EnvironmentGitRecoveryPageSize]
		out.NextBefore = ids[len(ids)-1]
	}
	releaseIDs := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		releaseIDs[i] = mustPgUUID(id)
	}
	protectionRows, err := sqlc.New().ListEnvironmentRecoveryProtection(ctx, tx, sqlc.ListEnvironmentRecoveryProtectionParams{SourceID: mustPgUUID(source.ID), ReleaseIds: releaseIDs})
	if err != nil {
		return out, err
	}
	protection := make([]recoveryProtection, 0, len(protectionRows))
	for _, r := range protectionRows {
		row := recoveryProtection{releaseID: r.ReleaseSetID, graphID: r.GraphID, until: r.ProtectedUntil.Time, bytes: r.Bytes, status: r.Status, available: r.Available}
		protection = append(protection, row)
		if row.status == "protected" {
			out.RetentionBytes += row.bytes
		}
	}

	for _, id := range ids {
		raw, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: params.AccountID, ProjectID: params.ProjectID, Environment: params.Environment, ReleaseID: mustPgUUID(id)})
		if err != nil {
			return out, mapErr(err)
		}
		release, err := decodeProjectReleaseSet(raw)
		if err != nil {
			return out, err
		}
		plan, err := s.previewEnvironmentGitOpsRollbackTx(ctx, tx, source, api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: id, TargetCommitSHA: request.TargetCommitSHA}, desired, out.ObservedAt)
		entry, err := recoveryRelease(release, plan, err, out.ObservedAt)
		if err != nil {
			return out, err
		}
		if out.RecoveryRetention != nil {
			entry.ProtectionStatus = "expired"
		}

		first, err := q.EnvironmentRecoveryReleaseFirstServing(ctx, tx, sqlc.EnvironmentRecoveryReleaseFirstServingParams{SourceID: mustPgUUID(source.ID), ReleaseID: mustPgUUID(id)})
		if err != nil {
			return out, err
		}
		if first.Valid {
			at := first.Time
			entry.ServedAt = &at
		}
		applyRecoveryProtection(&entry, protection)
		out.Releases = append(out.Releases, entry)
	}
	return out, nil
}
