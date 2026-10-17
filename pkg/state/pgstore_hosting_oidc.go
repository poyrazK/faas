package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingOIDCStore = (*PgStore)(nil)

func hostingOIDCTrustRow(r sqlc.HostingOidcTrust) api.HostingOIDCTrust {
	return api.HostingOIDCTrust{ID: uuidString(r.ID), AccountID: uuidString(r.AccountID), SiteID: uuidString(r.SiteID), CreatedAt: r.CreatedAt.Time, HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: r.RepositoryOwnerID, RepositoryID: r.RepositoryID, Repository: r.Repository, Ref: r.Ref, WorkflowRef: r.WorkflowRef, EventName: r.EventName, Environment: r.Environment}}
}

func (s *PgStore) CreateHostingOIDCTrust(ctx context.Context, t api.HostingOIDCTrust) (api.HostingOIDCTrust, error) {
	if !hosting.ValidHostingOIDCTrust(t.HostingOIDCTrustRules) || uuid.Validate(t.ID) != nil || t.CreatedAt.IsZero() {
		return t, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return t, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(t.AccountID)
	site, _ := parsePgUUID(t.SiteID)
	id, _ := parsePgUUID(t.ID)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: site})
	if err != nil {
		return t, hostingStoreError(err)
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return t, ErrConflict
	}
	count, err := q.HostingOIDCTrustCount(ctx, tx, site)
	if err != nil {
		return t, err
	}
	if count >= api.HostingOIDCTrustsPerSite {
		return t, &HostingQuotaError{Resource: "oidc_trusts", Limit: api.HostingOIDCTrustsPerSite, Observed: count + 1}
	}
	row, err := q.HostingOIDCTrustCreate(ctx, tx, sqlc.HostingOIDCTrustCreateParams{ID: id, AccountID: a, SiteID: site, RepositoryOwnerID: t.RepositoryOwnerID, RepositoryID: t.RepositoryID, Repository: t.Repository, Ref: t.Ref, WorkflowRef: t.WorkflowRef, EventName: t.EventName, Environment: t.Environment, CreatedAt: hostingTimestamp(t.CreatedAt)})
	if err != nil {
		return t, hostingStoreError(err)
	}
	return hostingOIDCTrustRow(row), tx.Commit(ctx)
}

func (s *PgStore) ListHostingOIDCTrusts(ctx context.Context, account, site string) ([]api.HostingOIDCTrust, error) {
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return nil, err
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingOIDCTrustList(ctx, s.pool, sqlc.HostingOIDCTrustListParams{AccountID: a, SiteID: si})
	if err != nil {
		return nil, err
	}
	out := make([]api.HostingOIDCTrust, 0, len(rows))
	for _, r := range rows {
		out = append(out, hostingOIDCTrustRow(r))
	}
	return out, nil
}

func (s *PgStore) GetHostingOIDCTrust(ctx context.Context, site, id string) (api.HostingOIDCTrust, error) {
	si, e := parsePgUUID(site)
	if e != nil {
		return api.HostingOIDCTrust{}, ErrNotFound
	}
	i, e := parsePgUUID(id)
	if e != nil {
		return api.HostingOIDCTrust{}, ErrNotFound
	}
	row, err := sqlc.New().HostingOIDCTrustGet(ctx, s.pool, sqlc.HostingOIDCTrustGetParams{SiteID: si, ID: i})
	return hostingOIDCTrustRow(row), hostingStoreError(err)
}

func (s *PgStore) RevokeHostingOIDCTrust(ctx context.Context, account, site, id string) error {
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	i, e := parsePgUUID(id)
	if e != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().HostingOIDCTrustRevoke(ctx, s.pool, sqlc.HostingOIDCTrustRevokeParams{AccountID: a, SiteID: si, ID: i})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *PgStore) CreateHostingOIDCCredential(ctx context.Context, expected api.HostingOIDCTrust, c api.HostingAutomationCredential) (api.HostingAutomationCredential, error) {
	if !validHostingCredential(c) || c.OIDCTrustID != expected.ID || c.AccountID != expected.AccountID || c.SiteID != expected.SiteID || len(c.Operations) != 2 {
		return c, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := parsePgUUID(c.AccountID)
	site, _ := parsePgUUID(c.SiteID)
	id, _ := parsePgUUID(c.ID)
	trust, _ := parsePgUUID(c.OIDCTrustID)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: a, ID: site})
	if err != nil {
		return c, hostingStoreError(err)
	}
	if !owned.DurableRouting || owned.Status != "active" || owned.Suspended || !c.ExpiresAt.After(time.Now()) {
		return c, ErrConflict
	}
	row, err := q.HostingOIDCTrustLock(ctx, tx, sqlc.HostingOIDCTrustLockParams{SiteID: site, ID: trust})
	if err != nil {
		return c, hostingStoreError(err)
	}
	if hostingOIDCTrustRow(row) != expected {
		return c, ErrConflict
	}
	if err = q.HostingCredentialPruneExpired(ctx, tx, site); err != nil {
		return c, err
	}
	count, err := q.HostingOIDCSessionCount(ctx, tx, site)
	if err != nil {
		return c, err
	}
	if count >= api.HostingOIDCSessionsPerSite {
		return c, &HostingQuotaError{Resource: "oidc_sessions", Limit: api.HostingOIDCSessionsPerSite, Observed: count + 1}
	}
	credential, err := q.HostingOIDCSessionCreate(ctx, tx, sqlc.HostingOIDCSessionCreateParams{ID: id, AccountID: a, SiteID: site, TokenHash: c.Hash, Label: c.Label, Operations: c.Operations, CreatedAt: hostingTimestamp(c.CreatedAt), ExpiresAt: hostingTimestamp(c.ExpiresAt), OidcTrustID: trust})
	if err != nil {
		return c, hostingStoreError(err)
	}
	return hostingCredentialRow(credential), tx.Commit(ctx)
}
