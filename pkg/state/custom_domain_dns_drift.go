package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CustomDomainDNSDriftStore is the optional state mutation used by the
// domain-doctor pass. It is deliberately separate from Store so narrow test
// doubles and downstream adapters remain source-compatible. The bool is true
// only for the first transition in a drift episode; subsequent observations
// refresh the timestamp without producing duplicate events.
type CustomDomainDNSDriftStore interface {
	MarkCustomDomainDNSDrifted(ctx context.Context, domain string, checkedAt time.Time, reason string) (transitioned bool, err error)
}

// MarkCustomDomainDNSDrifted atomically revokes verification for a domain that
// was previously verified. A domain already in dns_drifted is refreshed but
// does not count as a new transition. Unverified domains in another lifecycle
// state are left untouched.
func (s *PgStore) MarkCustomDomainDNSDrifted(ctx context.Context, domain string, checkedAt time.Time, reason string) (bool, error) {
	var verified bool
	var status string
	err := s.pool.QueryRow(ctx, `
		select verified_at is not null, cert_status
		  from custom_domains
		 where domain = $1`, domain).Scan(&verified, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !verified && CustomDomainCertStatus(status) != CustomDomainCertDNSDrifted {
		return false, nil
	}
	if !verified {
		_, err = s.pool.Exec(ctx, `
			update custom_domains
			   set dns_last_checked_at = $2,
			       cert_last_error = $3
			 where domain = $1
			   and verified_at is null
			   and cert_status = 'dns_drifted'`,
			domain, nullableTime(checkedAt), nullableStr(reason))
		return false, err
	}

	tag, err := s.pool.Exec(ctx, `
		update custom_domains
		   set verified_at = null,
		       cert_status = 'dns_drifted',
		       cert_expires_at = null,
		       cert_last_error = $2,
		       dns_last_checked_at = $3,
		       cert_failed_at = null,
		       last_cert_issuance_failed_email_at = null
		 where domain = $1
		   and verified_at is not null`,
		domain, nullableStr(reason), nullableTime(checkedAt))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkCustomDomainDNSDrifted mirrors the Postgres transition under the
// MemStore mutex so doctor tests exercise the same one-event-per-episode
// contract as production.
func (m *MemStore) MarkCustomDomainDNSDrifted(_ context.Context, domain string, checkedAt time.Time, reason string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return false, ErrNotFound
	}
	if !d.Verified() && d.CertStatus != CustomDomainCertDNSDrifted {
		return false, nil
	}
	transitioned := d.Verified()
	d.VerifiedAt = time.Time{}
	d.CertStatus = CustomDomainCertDNSDrifted
	d.CertExpiresAt = time.Time{}
	d.CertLastError = reason
	d.DNSLastCheckedAt = checkedAt
	d.CertFailedAt = time.Time{}
	d.CertFailureEmailAt = time.Time{}
	m.domains[domain] = d
	return transitioned, nil
}

var _ CustomDomainDNSDriftStore = (*PgStore)(nil)
var _ CustomDomainDNSDriftStore = (*MemStore)(nil)
