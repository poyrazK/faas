package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
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
	return api.HostingSite{ID: uuidString(r.ID), AccountID: uuidString(r.AccountID), ProjectID: uuidString(r.ProjectID), Environment: r.Environment, Name: r.Name, Hostname: r.Hostname, OriginID: r.OriginID, Status: r.Status, ActiveReleaseID: uuidString(r.ActiveReleaseID), Revision: r.Revision, CreatedAt: r.CreatedAt.Time, DurableRouting: r.DurableRouting, Suspended: r.Suspended}
}

func hostingReleaseRow(r sqlc.HostingRelease) (api.HostingRelease, error) {
	out := api.HostingRelease{LastActivityAt: r.LastActivityAt.Time, ArtifactBytes: r.ArtifactBytes, ID: uuidString(r.ID), SiteID: uuidString(r.SiteID), State: r.State, CreatedAt: r.CreatedAt.Time, RetirementGeneration: r.RetirementGeneration}
	if r.FirstPromotedAt.Valid {
		t := r.FirstPromotedAt.Time
		out.FirstPromotedAt = &t
	}
	if r.RetiredAt.Valid {
		t := r.RetiredAt.Time
		out.RetiredAt = &t
	}
	if r.RetirementPublishedAt.Valid {
		t := r.RetirementPublishedAt.Time
		out.RetirementPublishedAt = &t
	}
	if len(r.Provenance) > 0 {
		if err := json.Unmarshal(r.Provenance, &out.Provenance); err != nil {
			return out, err
		}
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
	r, err := q.HostingSiteCreate(ctx, tx, sqlc.HostingSiteCreateParams{ID: id, AccountID: a, ProjectID: p, Environment: site.Environment, Name: site.Name, Hostname: site.Hostname, OriginID: site.OriginID, DurableRouting: site.DurableRouting})
	if err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	if _, err = bumpHostingRoute(ctx, tx, id); err != nil {
		return api.HostingSite{}, err
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
	return s.CreateHostingReleaseWithProvenance(ctx, account, site, manifest, nil)
}
func (s *PgStore) CreateHostingReleaseWithProvenance(ctx context.Context, account, site string, manifest api.HostingManifest, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
	if hosting.ValidateProvenance(provenance) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	if validateHostingManifest(manifest) != nil {
		return api.HostingRelease{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingRelease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := s.createHostingReleaseTx(ctx, tx, account, site, manifest, provenance)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) createHostingReleaseTx(ctx context.Context, tx pgx.Tx, account, site string, manifest api.HostingManifest, provenance *api.HostingReleaseProvenance) (api.HostingRelease, error) {
	var err error
	a, _ := parsePgUUID(account)
	i, idErr := parsePgUUID(site)
	if idErr != nil {
		return api.HostingRelease{}, ErrNotFound
	}
	q := sqlc.New()
	// Serialize all sites in this account before checking shared capacity.
	if _, err = q.HostingAccountLock(ctx, tx, a); err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
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
	payload := hostingPayloadBytes(manifest)
	usage, err := q.HostingAccountUsage(ctx, tx, hostingUsageParams(account))
	if err != nil {
		return api.HostingRelease{}, err
	}
	if err := checkHostingAdmission(hostingUsageRow(account, usage), payload); err != nil {
		return api.HostingRelease{}, err
	}
	body, _ := json.Marshal(manifest)
	id, _ := parsePgUUID(uuid.NewString())
	r, err := q.HostingReleaseCreate(ctx, tx, sqlc.HostingReleaseCreateParams{ID: id, SiteID: i, Manifest: body, ArtifactBytes: payload})
	if err == nil && provenance != nil {
		data, _ := json.Marshal(provenance)
		var n int64
		n, err = q.HostingReleaseSetProvenance(ctx, tx, sqlc.HostingReleaseSetProvenanceParams{ID: id, Provenance: data})
		if err == nil && n != 1 {
			err = ErrConflict
		}
		r.Provenance = data
	}
	if err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	out, err := hostingReleaseRow(r)
	if err != nil {
		return api.HostingRelease{}, err
	}
	return out, nil
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
	previous, err := q.HostingReleaseGet(ctx, tx, sqlc.HostingReleaseGetParams{AccountID: a, SiteID: i, ID: rid})
	if err != nil {
		return api.HostingRelease{}, hostingStoreError(err)
	}
	r, err := q.HostingReleaseReady(ctx, tx, sqlc.HostingReleaseReadyParams{AccountID: a, SiteID: i, ID: rid})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingRelease{}, ErrConflict
	}
	if err != nil {
		return api.HostingRelease{}, err
	}
	if previous.State != "ready" {
		if _, err = bumpHostingRoute(ctx, tx, i); err != nil {
			return api.HostingRelease{}, err
		}
	}
	out, err := hostingReleaseRow(r)
	if err != nil {
		return api.HostingRelease{}, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) PromoteHostingRelease(ctx context.Context, account, site, release string, expected int64) (api.HostingSite, error) {
	return s.promoteHostingRelease(ctx, account, site, release, expected, nil)
}
func (s *PgStore) PromoteHostingReleaseRecorded(ctx context.Context, account, site, release string, expected int64, actor api.HostingPromotionActor) (api.HostingSite, error) {
	if !validHostingActor(actor) {
		return api.HostingSite{}, ErrInvalidArgument
	}
	return s.promoteHostingRelease(ctx, account, site, release, expected, &actor)
}
func (s *PgStore) promoteHostingRelease(ctx context.Context, account, site, release string, expected int64, actor *api.HostingPromotionActor) (api.HostingSite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingSite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	rid, _ := parsePgUUID(release)
	q := sqlc.New()
	previous, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: i})
	if err != nil {
		return api.HostingSite{}, hostingStoreError(err)
	}
	r, err := q.HostingReleasePromote(ctx, tx, sqlc.HostingReleasePromoteParams{AccountID: a, SiteID: i, ReleaseID: rid, ExpectedRevision: expected})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingSite{}, ErrConflict
	}
	if err != nil {
		return api.HostingSite{}, err
	}
	if count, markErr := q.HostingReleaseMarkPromoted(ctx, tx, sqlc.HostingReleaseMarkPromotedParams{SiteID: i, ID: rid}); markErr != nil || count != 1 {
		if markErr != nil {
			return api.HostingSite{}, markErr
		}
		return api.HostingSite{}, ErrConflict
	}
	if _, err = bumpHostingRoute(ctx, tx, i); err != nil {
		return api.HostingSite{}, err
	}
	if actor != nil {
		target, targetErr := q.HostingReleaseGet(ctx, tx, sqlc.HostingReleaseGetParams{AccountID: a, SiteID: i, ID: rid})
		if targetErr != nil {
			return api.HostingSite{}, targetErr
		}
		actorID, _ := parsePgUUID(actor.AccountID)
		keyID, _ := parsePgUUID(actor.KeyID)
		err = q.HostingPromotionRecord(ctx, tx, sqlc.HostingPromotionRecordParams{SiteID: i, Revision: r.Revision, PreviousRevision: previous.Revision, ReleaseID: rid, PreviousReleaseID: previous.ActiveReleaseID, ActorAccountID: actorID, ActorKeyID: keyID, Provenance: target.Provenance})
		if err != nil {
			return api.HostingSite{}, err
		}
		if err = q.HostingPromotionPrune(ctx, tx, sqlc.HostingPromotionPruneParams{SiteID: i, Retained: api.HostingHistoryPerSite}); err != nil {
			return api.HostingSite{}, err
		}
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
	generation, err := bumpHostingRoute(ctx, tx, i)
	if err != nil {
		return api.HostingRelease{}, err
	}
	r, err := q.HostingReleaseRetire(ctx, tx, sqlc.HostingReleaseRetireParams{AccountID: a, SiteID: i, ID: rid, Generation: generation})
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
	count, err := sqlc.New().HostingReleaseDeleted(ctx, s.pool, sqlc.HostingReleaseDeletedParams{AccountID: a, SiteID: i, ID: rid, SafeBefore: hostingTimestamp(time.Now().Add(-api.HostingRetirementGrace))})
	if err == nil && count == 0 {
		return ErrConflict
	}
	return err
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
	generation, err := bumpHostingRoute(ctx, tx, i)
	if err != nil {
		return api.HostingSite{}, err
	}
	if err = q.HostingSiteRetireReleases(ctx, tx, sqlc.HostingSiteRetireReleasesParams{SiteID: i, Generation: generation}); err != nil {
		return api.HostingSite{}, err
	}
	if err = q.HostingPreviewDeleteSite(ctx, tx, i); err != nil {
		return api.HostingSite{}, err
	}
	if err = q.HostingDomainDeleteSite(ctx, tx, i); err != nil {
		return api.HostingSite{}, err
	}
	return hostingSiteRow(r), tx.Commit(ctx)
}

func (s *PgStore) PurgeHostingSite(ctx context.Context, account, site string) error {
	a, _ := parsePgUUID(account)
	i, _ := parsePgUUID(site)
	count, err := sqlc.New().HostingSitePurge(ctx, s.pool, sqlc.HostingSitePurgeParams{AccountID: a, ID: i, SafeBefore: hostingTimestamp(time.Now().Add(-api.HostingRetirementGrace))})
	if err == nil && count == 0 {
		return ErrConflict
	}
	return err
}

func hostingTimePointer(at pgtype.Timestamptz) *time.Time {
	if !at.Valid {
		return nil
	}
	t := at.Time
	return &t
}
