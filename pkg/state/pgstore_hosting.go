package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingStore = (*PgStore)(nil)

func hostingStoreError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func hostingSiteRow(r sqlc.HostingSite) api.HostingSite {
	return api.HostingSite{ID: uuidString(r.ID), AccountID: uuidString(r.AccountID), ProjectID: uuidString(r.ProjectID), Environment: r.Environment, Name: r.Name, Hostname: r.Hostname, OriginID: r.OriginID, Status: r.Status, ActiveReleaseID: uuidString(r.ActiveReleaseID), Revision: r.Revision, CreatedAt: r.CreatedAt.Time}
}

func hostingReleaseRow(r sqlc.HostingRelease) (api.HostingRelease, error) {
	out := api.HostingRelease{ID: uuidString(r.ID), SiteID: uuidString(r.SiteID), State: r.State, CreatedAt: r.CreatedAt.Time}
	if r.RetiredAt.Valid {
		t := r.RetiredAt.Time
		out.RetiredAt = &t
	}
	err := json.Unmarshal(r.Manifest, &out.Manifest)
	return out, err
}

func hostingParentDeleteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" && strings.HasPrefix(pgErr.ConstraintName, "hosting_sites_") {
		return ErrConflict
	}
	return err
}

func (s *PgStore) CreateHostingSite(ctx context.Context, site api.HostingSite) (api.HostingSite, error) {
	if !validHostingSite(site) {
		return api.HostingSite{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingSite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(site.AccountID)
	p, _ := parsePgUUID(site.ProjectID)
	id, _ := parsePgUUID(site.ID)
	if _, err = q.HostingAccountLock(ctx, tx, a); err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	owned, err := q.HostingProjectEnvironmentOwned(ctx, tx, sqlc.HostingProjectEnvironmentOwnedParams{AccountID: a, ProjectID: p, Environment: site.Environment})
	if err != nil || !owned {
		return api.HostingSite{}, errors.Join(ErrNotFound, err)
	}
	count, err := q.HostingSiteCount(ctx, tx, a)
	if err != nil {
		return api.HostingSite{}, err
	}
	if count >= api.HostingSitesPerAccount {
		return api.HostingSite{}, hostingQuota(api.HostingSitesPerAccount)
	}
	r, err := q.HostingSiteCreate(ctx, tx, sqlc.HostingSiteCreateParams{ID: id, AccountID: a, ProjectID: p, Environment: site.Environment, Name: site.Name, Hostname: site.Hostname, OriginID: site.OriginID})
	if err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	return hostingSiteRow(r), tx.Commit(ctx)
}

func (s *PgStore) GetHostingSite(ctx context.Context, account, id string) (api.HostingSite, error) {
	a, err := parsePgUUID(account)
	i, idErr := parsePgUUID(id)
	if err != nil || idErr != nil {
		return api.HostingSite{}, ErrNotFound
	}
	r, err := sqlc.New().HostingSiteGet(ctx, s.pool, sqlc.HostingSiteGetParams{AccountID: a, ID: i})
	return hostingSiteRow(r), hostingStoreError(err)
}

func (s *PgStore) ListHostingSites(ctx context.Context, account string) ([]api.HostingSite, error) {
	a, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().HostingSiteList(ctx, s.pool, a)
	out := make([]api.HostingSite, 0, len(rows))
	for _, r := range rows {
		out = append(out, hostingSiteRow(r))
	}
	return out, hostingStoreError(err)
}

func (s *PgStore) ListHostingRoutes(ctx context.Context, after string, limit int) ([]api.HostingSite, error) {
	if limit < 1 || limit > api.HostingRoutePageSize || after != "" && uuid.Validate(after) != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().HostingRoutePage(ctx, s.pool, sqlc.HostingRoutePageParams{AfterID: after, PageSize: int32(limit)})
	out := make([]api.HostingSite, 0, len(rows))
	for _, r := range rows {
		out = append(out, hostingSiteRow(r))
	}
	return out, hostingStoreError(err)
}

func (s *PgStore) CreateHostingRelease(ctx context.Context, account, site string, manifest api.HostingManifest) (api.HostingRelease, error) {
	if validateHostingManifest(manifest) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingRelease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	i, idErr := parsePgUUID(site)
	if idErr != nil {
		return api.HostingRelease{}, ErrNotFound
	}
	q := sqlc.New()
	if _, err = q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: i}); err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	locked, err := q.HostingSiteGet(ctx, tx, sqlc.HostingSiteGetParams{AccountID: a, ID: i})
	if err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	if locked.Status != "active" {
		return api.HostingRelease{}, ErrConflict
	}
	count, err := q.HostingReleaseCount(ctx, tx, i)
	if err != nil {
		return api.HostingRelease{}, err
	}
	if count >= api.HostingReleasesPerSite {
		return api.HostingRelease{}, hostingQuota(api.HostingReleasesPerSite)
	}
	body, _ := json.Marshal(manifest)
	id, _ := parsePgUUID(uuid.NewString())
	r, err := q.HostingReleaseCreate(ctx, tx, sqlc.HostingReleaseCreateParams{ID: id, SiteID: i, Manifest: body})
	if err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	out, err := hostingReleaseRow(r)
	if err != nil {
		return api.HostingRelease{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) GetHostingRelease(ctx context.Context, account, site, id string) (api.HostingRelease, error) {
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(id)
	r, err := sqlc.New().HostingReleaseGet(ctx, s.pool, sqlc.HostingReleaseGetParams{AccountID: a, SiteID: i, ID: rid})
	if err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	return hostingReleaseRow(r)
}

func (s *PgStore) ListHostingReleases(ctx context.Context, account, site string) ([]api.HostingRelease, error) {
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return nil, err
	}
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingReleaseList(ctx, s.pool, sqlc.HostingReleaseListParams{AccountID: a, SiteID: i})
	out := make([]api.HostingRelease, 0, len(rows))
	for _, row := range rows {
		r, decodeErr := hostingReleaseRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		out = append(out, r)
	}
	return out, hostingStoreError(err)
}

func (s *PgStore) ReadyHostingRelease(ctx context.Context, account, site, id string) (api.HostingRelease, error) {
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(id)
	r, err := sqlc.New().HostingReleaseReady(ctx, s.pool, sqlc.HostingReleaseReadyParams{AccountID: a, SiteID: i, ID: rid})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingRelease{}, ErrConflict
	}
	if err != nil {
		return api.HostingRelease{}, err
	}
	return hostingReleaseRow(r)
}

func (s *PgStore) PromoteHostingRelease(ctx context.Context, account, site, release string, expected int64) (api.HostingSite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingSite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(release)
	q := sqlc.New()
	if _, err = q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: i}); err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	r, err := q.HostingReleasePromote(ctx, tx, sqlc.HostingReleasePromoteParams{AccountID: a, SiteID: i, ReleaseID: rid, ExpectedRevision: expected})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingSite{}, ErrConflict
	}
	if err != nil {
		return api.HostingSite{}, err
	}
	return hostingSiteRow(r), tx.Commit(ctx)
}

func (s *PgStore) RetireHostingRelease(ctx context.Context, account, site, id string) (api.HostingRelease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingRelease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(id)
	q := sqlc.New()
	if _, err = q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: i}); err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	r, err := q.HostingReleaseRetire(ctx, tx, sqlc.HostingReleaseRetireParams{AccountID: a, SiteID: i, ID: rid})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingRelease{}, ErrConflict
	}
	if err != nil {
		return api.HostingRelease{}, err
	}
	out, err := hostingReleaseRow(r)
	if err != nil {
		return api.HostingRelease{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) DeleteHostingReleaseRecord(ctx context.Context, account, site, id string) error {
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(id)
	return sqlc.New().HostingReleaseDeleted(ctx, s.pool, sqlc.HostingReleaseDeletedParams{AccountID: a, SiteID: i, ID: rid})
}

func (s *PgStore) DeleteHostingSite(ctx context.Context, account, site string) (api.HostingSite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingSite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	q := sqlc.New()
	locked, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: i})
	if err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	if locked.Status == "deleting" {
		return hostingSiteRow(locked), nil
	}
	r, err := q.HostingSiteDelete(ctx, tx, sqlc.HostingSiteDeleteParams{AccountID: a, ID: i})
	if err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	if err = q.HostingSiteRetireReleases(ctx, tx, i); err != nil {
		return api.HostingSite{}, err
	}
	return hostingSiteRow(r), tx.Commit(ctx)
}

func (s *PgStore) PurgeHostingSite(ctx context.Context, account, site string) error {
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	return sqlc.New().HostingSitePurge(ctx, s.pool, sqlc.HostingSitePurgeParams{AccountID: a, ID: i})
}
