package state

import (
	"context"
	"time"
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
	if claim, bound := CustomDomainProbe(ctx); bound {
		return s.markCustomDomainProbeDrifted(ctx, claim, domain, checkedAt, reason)
	}
	claim, err := s.DomainByName(ctx, domain)
	if err != nil {
		return false, err
	}
	return s.markCustomDomainProbeDrifted(ctx, claim, domain, checkedAt, reason)
}

// MarkCustomDomainDNSDrifted mirrors the Postgres transition under the
// MemStore mutex so doctor tests exercise the same one-event-per-episode
// contract as production.
func (m *MemStore) MarkCustomDomainDNSDrifted(ctx context.Context, domain string, checkedAt time.Time, reason string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return false, ErrNotFound
	}
	if !customDomainProbeMatches(ctx, d) {
		return false, ErrStaleCustomDomainProbe
	}
	if d.DNSLastCheckedAt.After(checkedAt) {
		return false, ErrStaleCustomDomainObservation
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
	if transitioned {
		d.VerificationNextCheckAt = time.Now()
		d.VerificationAttempts = 0
		d.VerificationExpiresAt = d.VerificationNextCheckAt.Add(7 * 24 * time.Hour)
	}
	m.domains[domain] = d
	return transitioned, nil
}

var _ CustomDomainDNSDriftStore = (*PgStore)(nil)
var _ CustomDomainDNSDriftStore = (*MemStore)(nil)
