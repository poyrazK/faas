package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CountFailedCertIssuancesSince counts legacy custom domains whose current
// failed episode started at or before since. The caller supplies the
// sustained-failure cutoff (F2 uses now-15m).
func (s *PgStore) CountFailedCertIssuancesSince(ctx context.Context, accountID, appID string, since time.Time) (int, error) {
	appArg := any(nil)
	if appID != "" {
		appArg = appID
	}
	var total int
	err := s.pool.QueryRow(ctx, `
		select count(*)::int
		  from custom_domains d
		  join apps a on a.id = d.app_id
		 where a.account_id = $1
		   and d.cert_status = 'failed'
		   and d.cert_failed_at is not null
		   and d.cert_failed_at <= $2
		   and ($3::uuid is null or d.app_id = $3::uuid)`,
		accountID, since.UTC(), appArg).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// CustomDomainCertFailureEmailCooldownStore is the durable per-domain gate
// for F2. It stays separate from Store so narrow test doubles remain source
// compatible while PgStore and MemStore provide the production implementation.
type CustomDomainCertFailureEmailCooldownStore interface {
	// ClaimCustomDomainCertFailureEmail atomically claims the next notification
	// slot. It returns false when the failure is younger than 15 minutes or the
	// domain was notified within the last 24 hours.
	ClaimCustomDomainCertFailureEmail(ctx context.Context, domain string, at time.Time) (bool, error)
}

// ClaimCustomDomainCertFailureEmail is the cross-replica serialization
// primitive for the F2 cooldown. The status and failure-age predicates live
// in the UPDATE so concurrent doctor passes cannot both win.
func (s *PgStore) ClaimCustomDomainCertFailureEmail(ctx context.Context, domain string, at time.Time) (bool, error) {
	if domain == "" {
		return false, ErrNotFound
	}
	var claimed string
	err := s.pool.QueryRow(ctx, `
		update custom_domains
		   set last_cert_issuance_failed_email_at = $2
		 where domain = $1
		   and cert_status = 'failed'
		   and cert_failed_at is not null
		   and cert_failed_at <= $2 - interval '15 minutes'
		   and (last_cert_issuance_failed_email_at is null
		        or last_cert_issuance_failed_email_at < $2 - interval '24 hours')
		 returning domain`, domain, at.UTC()).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return claimed != "", nil
}

// ClaimCustomDomainCertFailureEmail mirrors the Postgres atomic gate under
// the MemStore mutex. The timestamp is kept in the domain row so tests and
// the in-memory daemon observe the same lifecycle as production.
func (m *MemStore) ClaimCustomDomainCertFailureEmail(_ context.Context, domain string, at time.Time) (bool, error) {
	if domain == "" {
		return false, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return false, ErrNotFound
	}
	if d.CertStatus != CustomDomainCertFailed || d.CertFailedAt.IsZero() || d.CertFailedAt.After(at.UTC().Add(-15*time.Minute)) {
		return false, nil
	}
	if !d.CertFailureEmailAt.IsZero() && !d.CertFailureEmailAt.Before(at.UTC().Add(-24*time.Hour)) {
		return false, nil
	}
	d.CertFailureEmailAt = at.UTC()
	m.domains[domain] = d
	return true, nil
}
