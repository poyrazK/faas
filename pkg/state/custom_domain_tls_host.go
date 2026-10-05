package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// CustomDomainTLSHostStore records which hostnames below a wildcard custom
// domain the public edge has admitted for on-demand certificate issuance
// (ADR-520). The edge asks before it loads, obtains or renews a certificate,
// so a host admitted once is admitted again for free and only a hostname
// never seen before consumes the wildcard's issuance budget. gatewayd-public
// is the only writer; the rows are edge runtime state, not customer intent,
// and are removed with their wildcard custom domain.
//
// It is an optional seam outside Store so narrow test doubles stay
// source-compatible.
type CustomDomainTLSHostStore interface {
	// AdmitCustomDomainTLSHost reports whether host may hold a certificate
	// under the verified wildcard custom domain wildcardDomain. A host
	// admitted before is admitted without consuming budget. A new host is
	// recorded and admitted only while fewer than limit hosts were newly
	// admitted for wildcardDomain within window before now. An unverified
	// wildcard admits nothing; a missing one returns ErrNotFound.
	AdmitCustomDomainTLSHost(ctx context.Context, wildcardDomain, host string, now time.Time, window time.Duration, limit int) (bool, error)
}

type customDomainTLSHost struct {
	wildcardDomain string
	admittedAt     time.Time
}

func validateTLSHostAdmission(wildcardDomain, host string, limit int) error {
	if !IsWildcardCustomDomain(wildcardDomain) {
		return fmt.Errorf("%w: %q is not a wildcard custom domain", ErrInvalidArgument, wildcardDomain)
	}
	if !WildcardMatchesHost(wildcardDomain, host) {
		return fmt.Errorf("%w: %q is not below %q", ErrInvalidArgument, host, wildcardDomain)
	}
	if limit < 0 {
		return fmt.Errorf("%w: negative admission limit", ErrInvalidArgument)
	}
	return nil
}

// AdmitCustomDomainTLSHost implements CustomDomainTLSHostStore. The fast
// path is a single indexed read: every reload and renewal of an admitted
// host takes it. A new host locks the wildcard row so concurrent admissions
// for the same wildcard cannot overshoot the limit and a concurrent delete
// cannot orphan the insert.
func (s *PgStore) AdmitCustomDomainTLSHost(ctx context.Context, wildcardDomain, host string, now time.Time, window time.Duration, limit int) (bool, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	host = strings.ToLower(host)
	if err := validateTLSHostAdmission(wildcardDomain, host, limit); err != nil {
		return false, err
	}
	var known bool
	if err := s.pool.QueryRow(ctx,
		`select exists(
		   select 1 from custom_domain_tls_hosts h
		     join custom_domains d on d.domain = h.wildcard_domain
		    where h.host = $1 and d.verified_at is not null)`, host).Scan(&known); err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: lookup: %w", mapErr(err))
	}
	if known {
		return true, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var verified bool
	if err := tx.QueryRow(ctx,
		`select verified_at is not null from custom_domains where domain = $1 for no key update`,
		wildcardDomain).Scan(&verified); err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: lock wildcard: %w", mapErr(err))
	}
	if !verified {
		return false, nil
	}
	var recent int
	if err := tx.QueryRow(ctx,
		`select count(*) from custom_domain_tls_hosts where wildcard_domain = $1 and admitted_at > $2`,
		wildcardDomain, now.Add(-window)).Scan(&recent); err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: count: %w", mapErr(err))
	}
	if recent >= limit {
		return false, nil
	}
	// A row left under a wildcard that is no longer verified is re-pointed
	// at the wildcard that now routes the host and counts as a new
	// admission.
	if _, err := tx.Exec(ctx,
		`insert into custom_domain_tls_hosts (host, wildcard_domain, admitted_at) values ($1, $2, $3)
		 on conflict (host) do update set wildcard_domain = excluded.wildcard_domain, admitted_at = excluded.admitted_at`,
		host, wildcardDomain, now); err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: insert: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("admit custom-domain tls host: commit: %w", err)
	}
	return true, nil
}

// AdmitCustomDomainTLSHost implements CustomDomainTLSHostStore.
func (m *MemStore) AdmitCustomDomainTLSHost(_ context.Context, wildcardDomain, host string, now time.Time, window time.Duration, limit int) (bool, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	host = strings.ToLower(host)
	if err := validateTLSHostAdmission(wildcardDomain, host, limit); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customDomainTLSHosts == nil {
		m.customDomainTLSHosts = map[string]customDomainTLSHost{}
	}
	if prior, ok := m.customDomainTLSHosts[host]; ok {
		if d, live := m.domains[prior.wildcardDomain]; live && d.Verified() {
			return true, nil
		}
	}
	d, ok := m.domains[wildcardDomain]
	if !ok {
		return false, ErrNotFound
	}
	if !d.Verified() {
		return false, nil
	}
	recent := 0
	cutoff := now.Add(-window)
	for _, h := range m.customDomainTLSHosts {
		if h.wildcardDomain == wildcardDomain && h.admittedAt.After(cutoff) {
			recent++
		}
	}
	if recent >= limit {
		return false, nil
	}
	m.customDomainTLSHosts[host] = customDomainTLSHost{wildcardDomain: wildcardDomain, admittedAt: now}
	return true, nil
}

// dropCustomDomainTLSHostsLocked mirrors the ON DELETE CASCADE from
// custom_domains. Callers hold m.mu.
func (m *MemStore) dropCustomDomainTLSHostsLocked(domain string) {
	domain = strings.ToLower(domain)
	for host, h := range m.customDomainTLSHosts {
		if h.wildcardDomain == domain {
			delete(m.customDomainTLSHosts, host)
		}
	}
}
