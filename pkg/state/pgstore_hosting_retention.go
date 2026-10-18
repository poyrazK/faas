package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingRetentionStore = (*PgStore)(nil)

func hostingRetentionRow(r sqlc.HostingRetentionPolicy) api.HostingRetentionPolicy {
	at := r.UpdatedAt.Time
	return api.HostingRetentionPolicy{SiteID: uuidString(r.SiteID), Revision: r.Revision, Enabled: r.Enabled, HostingRetentionRules: api.HostingRetentionRules{PendingAgeSeconds: r.PendingAgeSeconds, StagedAgeSeconds: r.StagedAgeSeconds}, UpdatedAt: &at}
}
func (s *PgStore) hostingCleanupPlanTx(ctx context.Context, tx pgx.Tx, account, site string, override *api.HostingRetentionRules, now time.Time) (api.HostingCleanupPlan, error) {
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingCleanupPlan{}, hostingStoreError(err)
	}
	policy := defaultHostingRetentionPolicy(site)
	row, err := q.HostingRetentionGet(ctx, tx, si)
	if err == nil {
		policy = hostingRetentionRow(row)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.HostingCleanupPlan{}, err
	}
	rules := policy.HostingRetentionRules
	if override != nil {
		if !hosting.ValidRetentionRules(*override) {
			return api.HostingCleanupPlan{}, ErrInvalidArgument
		}
		rules = *override
	}
	rows, err := q.HostingReleaseList(ctx, tx, sqlc.HostingReleaseListParams{AccountID: a, SiteID: si})
	if err != nil {
		return api.HostingCleanupPlan{}, err
	}
	releases := make([]api.HostingRelease, 0, len(rows))
	for _, r := range rows {
		release, err := hostingReleaseRow(r)
		if err != nil {
			return api.HostingCleanupPlan{}, err
		}
		releases = append(releases, release)
	}
	ps, err := q.HostingPreviewList(ctx, tx, sqlc.HostingPreviewListParams{AccountID: a, SiteID: si})
	if err != nil {
		return api.HostingCleanupPlan{}, err
	}
	previews := make([]api.HostingPreview, 0, len(ps))
	for _, p := range ps {
		previews = append(previews, hostingPreviewRow(p))
	}
	runs, err := q.HostingRetentionCIExpiry(ctx, tx, si)
	if err != nil {
		return api.HostingCleanupPlan{}, err
	}
	expiry := map[string]time.Time{}
	for _, run := range runs {
		expiry[uuidString(run.ReleaseID)] = run.ExpiresAt.Time
	}
	return hosting.CleanupPlan(hostingSiteRow(owned), policy, rules, releases, previews, expiry, now), nil
}
func (s *PgStore) GetHostingCleanupPlan(ctx context.Context, account, site string, rules *api.HostingRetentionRules, now time.Time) (api.HostingCleanupPlan, error) {
	if now.IsZero() {
		return api.HostingCleanupPlan{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingCleanupPlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return s.hostingCleanupPlanTx(ctx, tx, account, site, rules, now)
}
func (s *PgStore) SetHostingRetentionPolicy(ctx context.Context, account, site string, req api.SetHostingRetentionRequest) (api.HostingRetentionPolicy, error) {
	if !validHostingRetentionRequest(req) {
		return api.HostingRetentionPolicy{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingRetentionPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingRetentionPolicy{}, hostingStoreError(err)
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingRetentionPolicy{}, ErrConflict
	}
	old, err := q.HostingRetentionGet(ctx, tx, si)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.HostingRetentionPolicy{}, err
	}
	if old.Revision != req.ExpectedRevision {
		return api.HostingRetentionPolicy{}, ErrConflict
	}
	row, err := q.HostingRetentionPut(ctx, tx, sqlc.HostingRetentionPutParams{SiteID: si, Enabled: req.Enabled, PendingAgeSeconds: req.PendingAgeSeconds, StagedAgeSeconds: req.StagedAgeSeconds})
	if err != nil {
		return api.HostingRetentionPolicy{}, err
	}
	return hostingRetentionRow(row), tx.Commit(ctx)
}
func (s *PgStore) ApplyHostingRetention(ctx context.Context, account, site, origin string, now time.Time) ([]string, error) {
	if now.IsZero() {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	plan, err := s.hostingCleanupPlanTx(ctx, tx, account, site, nil, now)
	if err != nil {
		return nil, err
	}
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	owned, err := q.HostingSiteGet(ctx, tx, sqlc.HostingSiteGetParams{AccountID: a, ID: si})
	if err != nil {
		return nil, hostingStoreError(err)
	}
	if owned.OriginID != origin {
		return nil, ErrConflict
	}
	ids := []string{}
	if !plan.Policy.Enabled {
		return ids, nil
	}
	for _, e := range plan.Entries {
		if !e.Eligible {
			continue
		}
		generation, err := bumpHostingRoute(ctx, tx, si)
		if err != nil {
			return nil, err
		}
		id, _ := parsePgUUID(e.ReleaseID)
		if _, err = q.HostingReleaseRetire(ctx, tx, sqlc.HostingReleaseRetireParams{AccountID: a, SiteID: si, ID: id, Generation: generation}); err != nil {
			return nil, hostingStoreError(err)
		}
		ids = append(ids, e.ReleaseID)
	}
	return ids, tx.Commit(ctx)
}
func (s *PgStore) ListHostingRetentionSites(ctx context.Context, after string, limit int) ([]api.HostingSite, error) {
	if limit <= 0 || limit > api.HostingRetentionSitesPerPass {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().HostingRetentionSitePage(ctx, s.pool, sqlc.HostingRetentionSitePageParams{AfterID: after, PageSize: int32(limit)})
	if err != nil {
		return nil, err
	}
	sites := make([]api.HostingSite, 0, len(rows))
	for _, r := range rows {
		sites = append(sites, hostingSiteRow(r))
	}
	return sites, nil
}
func (s *PgStore) TouchHostingRelease(ctx context.Context, account, site, id string) (api.HostingRelease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingRelease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	ri, _ := parsePgUUID(id)
	if _, err = q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si}); err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	row, err := q.HostingReleaseTouch(ctx, tx, sqlc.HostingReleaseTouchParams{AccountID: a, SiteID: si, ID: ri})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingRelease{}, ErrConflict
	}
	if err != nil {
		return api.HostingRelease{}, err
	}
	out, err := hostingReleaseRow(row)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
