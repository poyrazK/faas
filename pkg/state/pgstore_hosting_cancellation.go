package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CancelHostingCIRun(ctx context.Context, account, site, id string) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingCIRun{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingCIRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, _ := parsePgUUID(id)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingCIRun{}, hostingStoreError(err)
	}
	row, err := q.HostingCIRunGet(ctx, tx, sqlc.HostingCIRunGetParams{AccountID: a, SiteID: si, ID: i})
	if err != nil {
		return api.HostingCIRun{}, hostingStoreError(err)
	}
	run := hostingCIRunRow(row)
	if run.Canceled {
		return run, nil
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingCIRun{}, ErrConflict
	}
	releaseRow, err := q.HostingReleaseGet(ctx, tx, sqlc.HostingReleaseGetParams{AccountID: a, SiteID: si, ID: row.ReleaseID})
	var release *api.HostingRelease
	if err == nil {
		value, e := hostingReleaseRow(releaseRow)
		if e != nil {
			return api.HostingCIRun{}, e
		}
		release = &value
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.HostingCIRun{}, err
	}
	rows, err := q.HostingPreviewList(ctx, tx, sqlc.HostingPreviewListParams{AccountID: a, SiteID: si})
	if err != nil {
		return api.HostingCIRun{}, err
	}
	previews := make([]api.HostingPreview, 0, len(rows))
	for _, p := range rows {
		previews = append(previews, hostingPreviewRow(p))
	}
	if hostingCancellationProtected(hostingSiteRow(owned), run, release, previews) {
		return api.HostingCIRun{}, ErrHostingCancellationProtected
	}
	if err = q.HostingCICancelPreview(ctx, tx, sqlc.HostingCICancelPreviewParams{SiteID: si, ID: row.PreviewID}); err != nil {
		return api.HostingCIRun{}, err
	}
	if release != nil && (release.State == "pending" || release.State == "ready") {
		generation, e := bumpHostingRoute(ctx, tx, si)
		if e != nil {
			return api.HostingCIRun{}, e
		}
		n, e := q.HostingCICancelRetire(ctx, tx, sqlc.HostingCICancelRetireParams{SiteID: si, ReleaseID: row.ReleaseID, Generation: generation})
		if e != nil {
			return api.HostingCIRun{}, e
		}
		if n != 1 {
			return api.HostingCIRun{}, ErrConflict
		}
	}
	row, err = q.HostingCIRunCancel(ctx, tx, sqlc.HostingCIRunCancelParams{SiteID: si, ID: i})
	if err != nil {
		return api.HostingCIRun{}, err
	}
	return hostingCIRunRow(row), tx.Commit(ctx)
}
