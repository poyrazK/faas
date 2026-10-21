package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HostingDomainStore = (*PgStore)(nil)

func hostingDomainRow(row sqlc.HostingDomain) (api.HostingDomain, error) {
	d := api.HostingDomain{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), SiteID: uuidString(row.SiteID), Hostname: row.Hostname,
		ProviderOriginID: row.ProviderOriginID, Challenge: row.Challenge, State: row.State, Revision: row.Revision, Generation: row.Generation,
		PublishedGeneration: row.PublishedGeneration, NextAttemptAt: row.NextAttemptAt.Time, Attempts: row.Attempts, LastErrorCode: row.LastErrorCode, CreatedAt: row.CreatedAt.Time}
	if row.PublishedAt.Valid {
		at := row.PublishedAt.Time
		d.PublishedAt = &at
	}
	err := json.Unmarshal(row.Details, &d.Details)
	return d, err
}

func (s *PgStore) CreateHostingDomain(ctx context.Context, d api.HostingDomain) (api.HostingDomain, error) {
	if !validHostingDomainInput(d) {
		return api.HostingDomain{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.HostingDomain{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	account, _ := parsePgUUID(d.AccountID)
	site, _ := parsePgUUID(d.SiteID)
	id, _ := parsePgUUID(d.ID)
	owned, err := q.HostingSiteLock(ctx, tx, sqlc.HostingSiteLockParams{AccountID: account, ID: site})
	if err != nil {
		return api.HostingDomain{}, hostingStoreError(err)
	}
	if !owned.DurableRouting || owned.Status != "active" {
		return api.HostingDomain{}, ErrConflict
	}
	count, err := q.HostingDomainCount(ctx, tx, site)
	if err != nil {
		return api.HostingDomain{}, err
	}
	if count >= api.HostingDomainsPerSite {
		return api.HostingDomain{}, hostingQuota(api.HostingDomainsPerSite)
	}
	if err = q.HostingDomainReserve(ctx, tx, sqlc.HostingDomainReserveParams{Hostname: d.Hostname, DomainID: id}); err != nil {
		return api.HostingDomain{}, hostingStoreError(err)
	}
	details, _ := json.Marshal(api.HostingDomainDetails{TLSStatus: "pending", Records: []api.HostingDNSRecord{}})
	row, err := q.HostingDomainCreate(ctx, tx, sqlc.HostingDomainCreateParams{ID: id, AccountID: account, SiteID: site, Hostname: d.Hostname, ProviderOriginID: d.ProviderOriginID, Challenge: d.Challenge, Details: details})
	if err != nil {
		return api.HostingDomain{}, hostingStoreError(err)
	}
	out, err := hostingDomainRow(row)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ListHostingDomains(ctx context.Context, account, site string) ([]api.HostingDomain, error) {
	if _, err := s.GetHostingSite(ctx, account, site); err != nil {
		return nil, err
	}
	a, _ := parsePgUUID(account)
	id, _ := parsePgUUID(site)
	rows, err := sqlc.New().HostingDomainList(ctx, s.pool, sqlc.HostingDomainListParams{AccountID: a, SiteID: id})
	if err != nil {
		return nil, err
	}
	out := make([]api.HostingDomain, 0, len(rows))
	for _, row := range rows {
		d, err := hostingDomainRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *PgStore) DeleteHostingDomain(ctx context.Context, account, site, id string, revision int64) (api.HostingDomain, error) {
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	di, err := parsePgUUID(id)
	if err != nil || revision < 1 {
		return api.HostingDomain{}, ErrInvalidArgument
	}
	row, err := sqlc.New().HostingDomainDelete(ctx, s.pool, sqlc.HostingDomainDeleteParams{AccountID: a, SiteID: si, ID: di, Revision: revision})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.HostingDomain{}, ErrConflict
		}
		return api.HostingDomain{}, err
	}
	return hostingDomainRow(row)
}

func (s *PgStore) ClaimHostingDomains(ctx context.Context, now time.Time, token string) ([]HostingDomainLease, error) {
	if now.IsZero() || uuid.Validate(token) != nil {
		return nil, ErrInvalidArgument
	}
	t, _ := parsePgUUID(token)
	rows, err := sqlc.New().HostingDomainClaim(ctx, s.pool, sqlc.HostingDomainClaimParams{NowAt: hostingTimestamp(now), UntilAt: hostingTimestamp(now.Add(api.HostingDomainLease)), Token: t, PageSize: api.HostingDomainPageSize})
	if err != nil {
		return nil, err
	}
	out := make([]HostingDomainLease, 0, len(rows))
	for _, row := range rows {
		d, err := hostingDomainRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, HostingDomainLease{Domain: d, Token: token})
	}
	return out, nil
}

func (s *PgStore) SaveHostingDomain(ctx context.Context, lease HostingDomainLease, status string, details api.HostingDomainDetails) (HostingDomainLease, error) {
	if !validHostingDomainState(status, details) {
		return HostingDomainLease{}, ErrInvalidArgument
	}
	id, _ := parsePgUUID(lease.Domain.ID)
	token, _ := parsePgUUID(lease.Token)
	data, _ := json.Marshal(details)
	row, err := sqlc.New().HostingDomainSave(ctx, s.pool, sqlc.HostingDomainSaveParams{ID: id, Token: token, Revision: lease.Domain.Revision, Generation: lease.Domain.Generation, State: status, Details: data})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HostingDomainLease{}, ErrConflict
		}
		return HostingDomainLease{}, err
	}
	d, err := hostingDomainRow(row)
	return HostingDomainLease{Domain: d, Token: lease.Token}, err
}

func (s *PgStore) AcknowledgeHostingDomain(ctx context.Context, lease HostingDomainLease, at time.Time) error {
	if at.IsZero() {
		return ErrInvalidArgument
	}
	id, _ := parsePgUUID(lease.Domain.ID)
	token, _ := parsePgUUID(lease.Token)
	n, err := sqlc.New().HostingDomainAck(ctx, s.pool, sqlc.HostingDomainAckParams{ID: id, Token: token, Generation: lease.Domain.Generation, PublishedAt: hostingTimestamp(at)})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (s *PgStore) FinishHostingDomain(ctx context.Context, lease HostingDomainLease, at time.Time, code string) error {
	if at.IsZero() || !validHostingDomainError(code) {
		return ErrInvalidArgument
	}
	id, _ := parsePgUUID(lease.Domain.ID)
	token, _ := parsePgUUID(lease.Token)
	n, err := sqlc.New().HostingDomainFinish(ctx, s.pool, sqlc.HostingDomainFinishParams{ID: id, Token: token, Revision: lease.Domain.Revision, Generation: lease.Domain.Generation, NextAt: hostingTimestamp(hostingDomainNext(lease.Domain, at, code)), ErrorCode: code})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (s *PgStore) ReleaseHostingDomainBatch(ctx context.Context, token string) error {
	if uuid.Validate(token) != nil {
		return ErrInvalidArgument
	}
	t, _ := parsePgUUID(token)
	return sqlc.New().HostingDomainReleaseBatch(ctx, s.pool, t)
}

func (s *PgStore) PurgeHostingDomain(ctx context.Context, lease HostingDomainLease, now time.Time) error {
	id, _ := parsePgUUID(lease.Domain.ID)
	token, _ := parsePgUUID(lease.Token)
	n, err := sqlc.New().HostingDomainPurge(ctx, s.pool, sqlc.HostingDomainPurgeParams{ID: id, Token: token, Generation: lease.Domain.Generation, BeforeAt: hostingTimestamp(now.Add(-api.HostingDomainRemovalGrace))})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (s *PgStore) RecoverHostingDomain(ctx context.Context, account, site, id string, req api.RecoverHostingDomainRequest, now time.Time) (api.HostingDomain, error) {
	if uuid.Validate(req.ProviderID) != nil || req.ExpectedRevision < 1 || now.IsZero() {
		return api.HostingDomain{}, ErrInvalidArgument
	}
	a, _ := parsePgUUID(account)
	si, _ := parsePgUUID(site)
	di, _ := parsePgUUID(id)
	row, err := sqlc.New().HostingDomainRecover(ctx, s.pool, sqlc.HostingDomainRecoverParams{AccountID: a, SiteID: si, ID: di, Revision: req.ExpectedRevision, Hostname: req.ExpectedHostname, ProviderID: req.ProviderID, BeforeAt: hostingTimestamp(now.Add(-api.HostingDomainRecoveryDelay))})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.HostingDomain{}, ErrConflict
	}
	if err != nil {
		return api.HostingDomain{}, err
	}
	return hostingDomainRow(row)
}
