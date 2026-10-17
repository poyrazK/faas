package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingPreviewStore = (*PgStore)(nil)

func hostingPreviewRow(r sqlc.HostingPreview) api.HostingPreview {
	return api.HostingPreview{ID: uuidString(r.ID), AccountID: uuidString(r.AccountID), SiteID: uuidString(r.SiteID), ReleaseID: uuidString(r.ReleaseID), Hostname: r.Hostname, OriginID: r.OriginID, ExpiresAt: r.ExpiresAt.Time, Revoked: r.Revoked, Generation: r.Generation, PublishedGeneration: r.PublishedGeneration, PublishedAt: hostingTimePointer(r.PublishedAt), NextAttemptAt: r.NextAttemptAt.Time, LastErrorCode: r.LastErrorCode, CreatedAt: r.CreatedAt.Time}
}
func (s *PgStore) CreateHostingPreview(ctx context.Context, p api.HostingPreview) (api.HostingPreview, error) {
	if !validHostingPreview(p) {
		return api.HostingPreview{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingPreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(p.AccountID)
	if _, err = sqlc.New().HostingAccountLock(ctx, tx, a); err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	out, err := s.createHostingPreviewTx(ctx, tx, p)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) createHostingPreviewTx(ctx context.Context, tx pgx.Tx, p api.HostingPreview) (api.HostingPreview, error) {
	return s.createHostingPreviewWithReservationTx(ctx, tx, p, "")
}
func (s *PgStore) createHostingPreviewWithReservationTx(ctx context.Context, tx pgx.Tx, p api.HostingPreview, excludeRun string) (api.HostingPreview, error) {
	q := sqlc.New()
	a, _ := parsePgUUID(p.AccountID)
	si, _ := parsePgUUID(p.SiteID)
	id, _ := parsePgUUID(p.ID)
	ri, _ := parsePgUUID(p.ReleaseID)
	site, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	if !site.DurableRouting || site.Status != "active" || site.OriginID != p.OriginID || p.Hostname != "p-"+p.ID+strings.TrimPrefix(site.Hostname, p.SiteID) {
		return api.HostingPreview{}, ErrConflict
	}
	release, err := q.HostingReleaseGet(ctx, tx, sqlc.HostingReleaseGetParams{AccountID: a, SiteID: si, ID: ri})
	if err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	if release.State != "ready" {
		return api.HostingPreview{}, ErrConflict
	}
	capacity, err := hostingPreviewCapacityQuery(ctx, tx, p.AccountID, p.SiteID, excludeRun, time.Now().UTC())
	if err != nil {
		return api.HostingPreview{}, err
	}
	if err = requireHostingPreviewCapacity(capacity); err != nil {
		return api.HostingPreview{}, err
	}
	row, err := q.HostingPreviewCreate(ctx, tx, sqlc.HostingPreviewCreateParams{ID: id, AccountID: a, SiteID: si, ReleaseID: ri, Hostname: p.Hostname, OriginID: p.OriginID, ExpiresAt: hostingTimestamp(p.ExpiresAt), CreatedAt: hostingTimestamp(p.CreatedAt)})
	if err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	return hostingPreviewRow(row), nil
}
func (s *PgStore) ListHostingPreviews(ctx context.Context, account, site string) ([]api.HostingPreview, error) {
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return nil, err
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingPreviewList(ctx, s.pool, sqlc.HostingPreviewListParams{AccountID: a, SiteID: si})
	if err != nil {
		return nil, err
	}
	out := make([]api.HostingPreview, 0, len(rows))
	for _, r := range rows {
		out = append(out, hostingPreviewRow(r))
	}
	return out, nil
}
func (s *PgStore) RevokeHostingPreview(ctx context.Context, account, site, id string, generation int64) (api.HostingPreview, error) {
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	pi, err := parsePgUUID(id)
	if err != nil || generation != 1 {
		return api.HostingPreview{}, ErrInvalidArgument
	}
	row, err := sqlc.New().HostingPreviewRevoke(ctx, s.pool, sqlc.HostingPreviewRevokeParams{AccountID: a, SiteID: si, ID: pi, Generation: generation})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingPreview{}, ErrConflict
	}
	return hostingPreviewRow(row), err
}
func (s *PgStore) DueHostingPreviews(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := sqlc.New().HostingPreviewDue(ctx, s.pool, sqlc.HostingPreviewDueParams{NowAt: hostingTimestamp(now), PageSize: api.HostingPreviewPageSize})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range rows {
		out = append(out, uuidString(id))
	}
	return out, nil
}
func (s *PgStore) PrepareHostingPreview(ctx context.Context, id string, now time.Time) (api.HostingPreview, error) {
	pi, err := parsePgUUID(id)
	if err != nil {
		return api.HostingPreview{}, ErrInvalidArgument
	}
	row, err := sqlc.New().HostingPreviewPrepare(ctx, s.pool, sqlc.HostingPreviewPrepareParams{ID: pi, NowAt: hostingTimestamp(now)})
	return hostingPreviewRow(row), hostingStoreError(err)
}
func (s *PgStore) FinishHostingPreview(ctx context.Context, p api.HostingPreview, now time.Time, code string) error {
	if now.IsZero() || !validHostingPreviewCode(code) {
		return ErrInvalidArgument
	}
	id, _ := parsePgUUID(p.ID)
	n, err := sqlc.New().HostingPreviewFinish(ctx, s.pool, sqlc.HostingPreviewFinishParams{ID: id, Generation: p.Generation, NowAt: hostingTimestamp(now), NextAt: hostingTimestamp(hostingPreviewNext(p, now, code)), ErrorCode: code})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}
func (s *PgStore) PurgeHostingPreview(ctx context.Context, p api.HostingPreview, now time.Time) error {
	id, _ := parsePgUUID(p.ID)
	n, err := sqlc.New().HostingPreviewPurge(ctx, s.pool, sqlc.HostingPreviewPurgeParams{ID: id, Generation: p.Generation, BeforeAt: hostingTimestamp(now.Add(-api.HostingPreviewRemovalGrace))})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}
