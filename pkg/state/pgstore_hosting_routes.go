package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func hostingTimestamp(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at.UTC(), Valid: true}
}

func hostingTime(at pgtype.Timestamptz) time.Time {
	if at.Valid {
		return at.Time
	}
	return time.Time{}
}

func bumpHostingRoute(ctx context.Context, db sqlc.DBTX, site pgtype.UUID) (int64, error) {
	generation, err := sqlc.New().HostingRouteChanged(ctx, db, site)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	} // Leased sites have no outbox.
	return generation, err
}

func (s *PgStore) SetHostingSiteSuspended(ctx context.Context, account, site string, expected int64, suspended bool) (api.HostingSite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingSite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	id, _ := parsePgUUID(site)
	q := sqlc.New()
	if _, err = q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: id}); err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	row, err := q.HostingSiteSuspend(ctx, tx, sqlc.HostingSiteSuspendParams{AccountID: a, SiteID: id, ExpectedRevision: expected, Suspended: suspended})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingSite{}, ErrConflict
	}
	if err != nil {
		return api.HostingSite{}, err
	}
	if _, err = bumpHostingRoute(ctx, tx, id); err != nil {
		return api.HostingSite{}, err
	}
	return hostingSiteRow(row), tx.Commit(ctx)
}

func (s *PgStore) ListDueHostingRoutePublications(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit < 1 || limit > api.HostingRoutePageSize {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().HostingRouteDue(ctx, s.pool, sqlc.HostingRouteDueParams{NowAt: hostingTimestamp(now), PageSize: int32(limit)})
	ids := make([]string, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, uuidString(id))
	}
	return ids, err
}

func (s *PgStore) HostingRouteSnapshot(ctx context.Context, site string) (HostingRouteSnapshot, error) {
	// A repeatable-read snapshot prevents a new release set being paired with an
	// old generation when ready/retire/promotion races a publisher.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return HostingRouteSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, idErr := parsePgUUID(site)
	if idErr != nil {
		return HostingRouteSnapshot{}, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.HostingRouteSiteGet(ctx, tx, id)
	if err != nil {
		return HostingRouteSnapshot{}, hostingStoreError(err)
	}
	publication, err := q.HostingRoutePublicationGet(ctx, tx, id)
	if err != nil {
		return HostingRouteSnapshot{}, hostingStoreError(err)
	}
	releases, err := q.HostingRoutingReleaseList(ctx, tx, id)
	if err != nil {
		return HostingRouteSnapshot{}, err
	}
	out := hostingPublicationSnapshot(hostingSiteRow(row), hostingPublication{
		Generation: publication.Generation, PublishedGeneration: publication.PublishedGeneration,
		PublishedAt: hostingTime(publication.PublishedAt), PendingSince: hostingTime(publication.PendingSince),
		NextAttemptAt: hostingTime(publication.NextAttemptAt), LastAttemptAt: hostingTime(publication.LastAttemptAt),
		RetryCount: publication.RetryCount, LastErrorCode: publication.LastErrorCode})
	for _, release := range releases {
		out.Releases = append(out.Releases, api.HostingRelease{ID: uuidString(release.ID), SiteID: uuidString(release.SiteID), State: release.State, FirstPromotedAt: hostingTimePointer(release.FirstPromotedAt)})
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) AcknowledgeHostingRoute(ctx context.Context, site string, generation int64, at time.Time) error {
	if generation < 1 || at.IsZero() {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, _ := parsePgUUID(site)
	q := sqlc.New()
	// Match lifecycle lock ordering: site, outbox, then releases.
	if _, err = q.HostingRouteSiteLock(ctx, tx, id); err != nil {
		return hostingStoreError(err)
	}
	publication, err := q.HostingRoutePublicationLock(ctx, tx, id)
	if err != nil {
		return hostingStoreError(err)
	}
	if generation > publication.Generation {
		return ErrConflict
	}
	if err = q.HostingRouteAcknowledged(ctx, tx, sqlc.HostingRouteAcknowledgedParams{SiteID: id, Generation: generation, PublishedAt: hostingTimestamp(at), NextAttemptAt: hostingTimestamp(at.Add(api.HostingRouteRepairInterval))}); err != nil {
		return err
	}
	if err = q.HostingRetirementAcknowledged(ctx, tx, sqlc.HostingRetirementAcknowledgedParams{SiteID: id, Generation: generation, PublishedAt: hostingTimestamp(at)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RetryHostingRoute(ctx context.Context, site string, generation int64, at time.Time, code string) error {
	if generation < 1 || at.IsZero() || !validHostingRouteFailure(code) {
		return ErrInvalidArgument
	}
	id, _ := parsePgUUID(site)
	return sqlc.New().HostingRouteRetry(ctx, s.pool, sqlc.HostingRouteRetryParams{SiteID: id, Generation: generation,
		NextAttemptAt: hostingTimestamp(at.Add(api.HostingRouteRetryInterval)), AttemptedAt: hostingTimestamp(at), ErrorCode: code})
}

func (s *PgStore) HostingRouteBacklog(ctx context.Context) (HostingRouteBacklog, error) {
	row, err := sqlc.New().HostingRouteBacklog(ctx, s.pool)
	return HostingRouteBacklog{Pending: row.Pending, OldestPendingAt: hostingTime(row.OldestPendingAt)}, err
}
