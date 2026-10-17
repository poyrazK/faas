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

var _ HostingAutomationStore = (*PgStore)(nil)

func hostingCredentialRow(r sqlc.HostingAutomationCredential) api.HostingAutomationCredential {
	c := api.HostingAutomationCredential{ID: uuidString(r.ID), AccountID: uuidString(r.AccountID), SiteID: uuidString(r.SiteID), Label: r.Label, Operations: r.Operations, Hash: r.TokenHash, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time}
	if r.OidcTrustID.Valid {
		c.OIDCTrustID = uuidString(r.OidcTrustID)
	}
	return c
}
func hostingCIRunRow(r sqlc.HostingCiRun) api.HostingCIRun {
	return api.HostingCIRun{ID: uuidString(r.ID), SiteID: uuidString(r.SiteID), ReleaseID: uuidString(r.ReleaseID), PreviewID: uuidString(r.PreviewID), Canceled: r.Canceled, PreviewCreated: r.PreviewCreated, PreviewReserved: r.PreviewReserved, PreviewExpiresAt: r.PreviewExpiresAt.Time, Fingerprint: r.Fingerprint, CreatedAt: r.CreatedAt.Time}
}
func (s *PgStore) CreateHostingAutomationCredential(ctx context.Context, c api.HostingAutomationCredential) (api.HostingAutomationCredential, error) {
	if c.OIDCTrustID != "" || !validHostingCredential(c) {
		return c, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(c.AccountID)
	si, _ := parsePgUUID(c.SiteID)
	id, _ := parsePgUUID(c.ID)
	site, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return c, hostingStoreError(err)
	}
	if !site.DurableRouting || site.Status != "active" {
		return c, ErrConflict
	}
	if err = q.HostingCredentialPruneExpired(ctx, tx, si); err != nil {
		return c, err
	}
	count, err := q.HostingCredentialCount(ctx, tx, si)
	if err != nil {
		return c, err
	}
	if count >= api.HostingAutomationCredentialsPerSite {
		return c, &HostingQuotaError{Limit: api.HostingAutomationCredentialsPerSite, Observed: api.HostingAutomationCredentialsPerSite + 1, Resource: "automation_credentials"}
	}
	row, err := q.HostingCredentialCreate(ctx, tx, sqlc.HostingCredentialCreateParams{ID: id, AccountID: a, SiteID: si, TokenHash: c.Hash, Label: c.Label, Operations: c.Operations, CreatedAt: hostingTimestamp(c.CreatedAt), ExpiresAt: hostingTimestamp(c.ExpiresAt)})
	if err != nil {
		return c, hostingStoreError(err)
	}
	return hostingCredentialRow(row), tx.Commit(ctx)
}
func (s *PgStore) ListHostingAutomationCredentials(ctx context.Context, account, site string) ([]api.HostingAutomationCredential, error) {
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return nil, err
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingCredentialList(ctx, s.pool, sqlc.HostingCredentialListParams{AccountID: a, SiteID: si})
	if err != nil {
		return nil, err
	}
	out := make([]api.HostingAutomationCredential, 0, len(rows))
	for _, r := range rows {
		c := hostingCredentialRow(r)
		c.Hash = nil
		out = append(out, c)
	}
	return out, nil
}
func (s *PgStore) RevokeHostingAutomationCredential(ctx context.Context, account, site, id string) error {
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, err := parsePgUUID(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().HostingCredentialRevoke(ctx, s.pool, sqlc.HostingCredentialRevokeParams{AccountID: a, SiteID: si, ID: i})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *PgStore) AuthenticateHostingAutomationToken(ctx context.Context, hash []byte) (Account, APIKey, error) {
	row, err := sqlc.New().HostingCredentialAuthenticate(ctx, s.pool, hash)
	if err != nil {
		return Account{}, APIKey{}, hostingStoreError(err)
	}
	c := hostingCredentialRow(row)
	account, err := s.AccountByID(ctx, c.AccountID)
	return account, hostingCredentialKey(c), err
}
func (s *PgStore) CreateHostingCIRun(ctx context.Context, account, site, id string, req api.HostingCIRunRequest) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) || !hosting.ValidCIRequest(req) {
		return api.HostingCIRun{}, ErrInvalidArgument
	}
	fingerprint, _ := hosting.CIFingerprint(req)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingCIRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, _ := parsePgUUID(id)
	if _, err = q.HostingAccountLock(ctx, tx, a); err != nil {
		return api.HostingCIRun{}, hostingStoreError(err)
	}
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingCIRun{}, hostingStoreError(err)
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingCIRun{}, ErrConflict
	}
	old, err := q.HostingCIRunGet(ctx, tx, sqlc.HostingCIRunGetParams{AccountID: a, SiteID: si, ID: i})
	if err == nil {
		run := hostingCIRunRow(old)
		if run.Fingerprint != fingerprint {
			return run, ErrConflict
		}
		return run, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.HostingCIRun{}, err
	}
	count, err := q.HostingCIRunCount(ctx, tx, si)
	if err != nil {
		return api.HostingCIRun{}, err
	}
	if count >= api.HostingCIRunsPerSite {
		return api.HostingCIRun{}, &HostingQuotaError{Limit: api.HostingCIRunsPerSite, Observed: api.HostingCIRunsPerSite + 1, Resource: "ci_runs"}
	}
	if req.ReservePreview {
		capacity, err := hostingPreviewCapacityQuery(ctx, tx, account, site, "", time.Now().UTC())
		if err != nil {
			return api.HostingCIRun{}, err
		}
		if err = requireHostingPreviewCapacity(capacity); err != nil {
			return api.HostingCIRun{}, err
		}
	}
	release, err := s.createHostingReleaseTx(ctx, tx, account, site, req.Manifest, req.Provenance)
	if err != nil {
		return api.HostingCIRun{}, err
	}
	run := newCIRun(id, site, release.ID, fingerprint, req.PreviewTTLSeconds)
	rid, _ := parsePgUUID(run.ReleaseID)
	pid, _ := parsePgUUID(run.PreviewID)
	row, err := q.HostingCIRunCreate(ctx, tx, sqlc.HostingCIRunCreateParams{SiteID: si, ID: i, ReleaseID: rid, PreviewID: pid, Fingerprint: fingerprint, CreatedAt: hostingTimestamp(run.CreatedAt), PreviewExpiresAt: hostingTimestamp(run.PreviewExpiresAt), PreviewReserved: req.ReservePreview})
	if err != nil {
		return run, err
	}
	return hostingCIRunRow(row), tx.Commit(ctx)
}
func (s *PgStore) GetHostingCIRun(ctx context.Context, account, site, id string) (api.HostingCIRun, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingCIRun{}, ErrNotFound
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, err := parsePgUUID(id)
	if err != nil {
		return api.HostingCIRun{}, ErrNotFound
	}
	row, err := sqlc.New().HostingCIRunGet(ctx, s.pool, sqlc.HostingCIRunGetParams{AccountID: a, SiteID: si, ID: i})
	return hostingCIRunRow(row), hostingStoreError(err)
}
func (s *PgStore) CreateHostingCIPreview(ctx context.Context, account, site, id string) (api.HostingPreview, error) {
	if !hosting.ValidCIRunID(id) {
		return api.HostingPreview{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingPreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, _ := parsePgUUID(id)
	if _, err = q.HostingAccountLock(ctx, tx, a); err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: si})
	if err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	row, err := q.HostingCIRunGet(ctx, tx, sqlc.HostingCIRunGetParams{AccountID: a, SiteID: si, ID: i})
	if err != nil {
		return api.HostingPreview{}, hostingStoreError(err)
	}
	run := hostingCIRunRow(row)
	if run.Canceled {
		return api.HostingPreview{}, ErrConflict
	}
	if run.PreviewCreated {
		rows, listErr := q.HostingPreviewList(ctx, tx, sqlc.HostingPreviewListParams{AccountID: a, SiteID: si})
		if listErr != nil {
			return api.HostingPreview{}, listErr
		}
		for _, p := range rows {
			if uuidString(p.ID) == run.PreviewID {
				return hostingPreviewRow(p), nil
			}
		}
		return api.HostingPreview{}, ErrConflict
	}
	if !run.PreviewExpiresAt.After(time.Now()) {
		return api.HostingPreview{}, ErrConflict
	}
	p, err := s.createHostingPreviewWithReservationTx(ctx, tx, ciPreview(run, hostingSiteRow(owned)), run.ID)
	if err != nil {
		return p, err
	}
	if err = q.HostingCIRunPreviewCreated(ctx, tx, sqlc.HostingCIRunPreviewCreatedParams{SiteID: si, ID: i}); err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}
