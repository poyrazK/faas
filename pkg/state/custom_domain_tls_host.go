package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/net/publicsuffix"
)

// CustomDomainTLSHostStore records which hostnames below a wildcard custom
// domain the public edge has admitted for on-demand certificate issuance
// (ADR-520). The edge asks before it loads, obtains or renews a certificate,
// so a host admitted once is admitted again for free and only a hostname
// never seen before consumes issuance budget. gatewayd-public is the only
// writer; the rows are edge runtime state, not customer intent. The host set
// is removed with its wildcard custom domain; the admission ledger the
// budgets count is not (ADR-520 amendment 1).
//
// It is an optional seam outside Store so narrow test doubles stay
// source-compatible.
type CustomDomainTLSHostStore interface {
	// AdmitCustomDomainTLSHost reports whether a.Host may hold a
	// certificate under the verified wildcard custom domain a.Wildcard. A
	// host admitted before is admitted without consuming budget. A new host
	// is recorded and admitted only while every cap in a has room. An
	// unverified wildcard admits nothing; a missing one returns ErrNotFound.
	AdmitCustomDomainTLSHost(ctx context.Context, a CustomDomainTLSAdmission) (CustomDomainTLSVerdict, error)
}

// CustomDomainTLSAdmission is one request to admit a host. Each cap counts
// the hosts newly admitted within its window, including those whose
// wildcard has since been deleted.
type CustomDomainTLSAdmission struct {
	Wildcard string
	Host     string
	Now      time.Time
	// Window is the rolling window of PerRegisteredDomain and PerOwner.
	// PerRegisteredDomain counts admissions under the wildcard's registered
	// domain (public suffix + 1) across sibling wildcards; PerOwner counts
	// them for the wildcard app's organization, else its account.
	Window              time.Duration
	PerRegisteredDomain int
	PerOwner            int
	// FleetWindow is the rolling window of FleetLimit, which counts every
	// admission in the deployment.
	FleetWindow time.Duration
	FleetLimit  int
}

// CustomDomainTLSVerdict is the outcome of AdmitCustomDomainTLSHost.
type CustomDomainTLSVerdict int

const (
	TLSHostAdmitted CustomDomainTLSVerdict = iota
	TLSHostWildcardUnverified
	TLSHostRegisteredDomainFull
	TLSHostOwnerFull
	TLSHostFleetFull
)

type customDomainTLSHost struct {
	wildcardDomain string
	admittedAt     time.Time
}

// customDomainTLSAdmission mirrors one custom_domain_tls_admissions row.
type customDomainTLSAdmission struct {
	host             string
	registeredDomain string
	ownerID          string
	admittedAt       time.Time
}

func normalizeTLSHostAdmission(a CustomDomainTLSAdmission) (CustomDomainTLSAdmission, error) {
	a.Wildcard = strings.ToLower(a.Wildcard)
	a.Host = strings.ToLower(a.Host)
	if !IsWildcardCustomDomain(a.Wildcard) {
		return a, fmt.Errorf("%w: %q is not a wildcard custom domain", ErrInvalidArgument, a.Wildcard)
	}
	if !WildcardMatchesHost(a.Wildcard, a.Host) {
		return a, fmt.Errorf("%w: %q is not below %q", ErrInvalidArgument, a.Host, a.Wildcard)
	}
	if a.PerRegisteredDomain < 0 || a.PerOwner < 0 || a.FleetLimit < 0 || a.Window <= 0 || a.FleetWindow <= 0 {
		return a, fmt.Errorf("%w: negative admission limit or empty window", ErrInvalidArgument)
	}
	return a, nil
}

// tlsRegisteredDomain is the registered domain (public suffix + 1) of a
// wildcard's hosts, the unit Let's Encrypt rate-limits. A wildcard whose
// suffix is itself a public suffix has none; its suffix stands in.
func tlsRegisteredDomain(wildcard string) string {
	suffix := strings.TrimPrefix(wildcard, "*.")
	if reg, err := publicsuffix.EffectiveTLDPlusOne(suffix); err == nil {
		return reg
	}
	return suffix
}

// verdict applies the caps, most specific first, to the recent counts.
func (a CustomDomainTLSAdmission) verdict(fleet, registered, owner int) CustomDomainTLSVerdict {
	switch {
	case registered >= a.PerRegisteredDomain:
		return TLSHostRegisteredDomainFull
	case owner >= a.PerOwner:
		return TLSHostOwnerFull
	case fleet >= a.FleetLimit:
		return TLSHostFleetFull
	}
	return TLSHostAdmitted
}

// ledgerCutoff is the oldest admission any cap still counts.
func (a CustomDomainTLSAdmission) ledgerCutoff() time.Time {
	return a.Now.Add(-max(a.Window, a.FleetWindow))
}

// AdmitCustomDomainTLSHost implements CustomDomainTLSHostStore. The fast
// path is a single indexed read: every reload and renewal of an admitted
// host takes it. A new host locks the wildcard row so a concurrent delete
// cannot orphan the insert, then takes one transaction advisory lock so
// admissions under different wildcards cannot overshoot the shared owner
// and fleet caps. The ask endpoint's lookup bucket bounds new-host
// attempts, so the lock is never hot.
func (s *pgCore) AdmitCustomDomainTLSHost(ctx context.Context, a CustomDomainTLSAdmission) (CustomDomainTLSVerdict, error) {
	a, err := normalizeTLSHostAdmission(a)
	if err != nil {
		return 0, err
	}
	var known bool
	if err := s.pool.QueryRow(ctx,
		`select exists(
		   select 1 from custom_domain_tls_hosts h
		     join custom_domains d on d.domain = h.wildcard_domain
		    where h.host = $1 and d.verified_at is not null)`, a.Host).Scan(&known); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: lookup: %w", mapErr(err))
	}
	if known {
		return TLSHostAdmitted, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var verified bool
	var ownerID string
	if err := tx.QueryRow(ctx,
		`select d.verified_at is not null, coalesce(a.org_id, a.account_id)::text
		   from custom_domains d join apps a on a.id = d.app_id
		  where d.domain = $1 for no key update of d`,
		a.Wildcard).Scan(&verified, &ownerID); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: lock wildcard: %w", mapErr(err))
	}
	if !verified {
		return TLSHostWildcardUnverified, nil
	}
	if _, err := tx.Exec(ctx,
		`select pg_advisory_xact_lock(hashtextextended('custom_domain_tls_admissions', 0))`); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: lock ledger: %w", mapErr(err))
	}
	if _, err := tx.Exec(ctx,
		`delete from custom_domain_tls_admissions where admitted_at <= $1`, a.ledgerCutoff()); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: prune: %w", mapErr(err))
	}
	registered := tlsRegisteredDomain(a.Wildcard)
	var fleet, perRegistered, perOwner int
	if err := tx.QueryRow(ctx,
		`select
		   (select count(*) from custom_domain_tls_admissions where admitted_at > $1),
		   (select count(*) from custom_domain_tls_admissions where registered_domain = $3 and admitted_at > $2),
		   (select count(*) from custom_domain_tls_admissions where owner_id = $4::uuid and admitted_at > $2)`,
		a.Now.Add(-a.FleetWindow), a.Now.Add(-a.Window), registered, ownerID).Scan(&fleet, &perRegistered, &perOwner); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: count: %w", mapErr(err))
	}
	if v := a.verdict(fleet, perRegistered, perOwner); v != TLSHostAdmitted {
		return v, nil
	}
	// A row left under a wildcard that is no longer verified is re-pointed
	// at the wildcard that now routes the host and counts as a new
	// admission.
	if _, err := tx.Exec(ctx,
		`insert into custom_domain_tls_hosts (host, wildcard_domain, admitted_at) values ($1, $2, $3)
		 on conflict (host) do update set wildcard_domain = excluded.wildcard_domain, admitted_at = excluded.admitted_at`,
		a.Host, a.Wildcard, a.Now); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: insert: %w", mapErr(err))
	}
	if _, err := tx.Exec(ctx,
		`insert into custom_domain_tls_admissions (host, registered_domain, owner_id, admitted_at)
		 values ($1, $2, $3::uuid, $4) on conflict do nothing`,
		a.Host, registered, ownerID, a.Now); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: record admission: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("admit custom-domain tls host: commit: %w", err)
	}
	return TLSHostAdmitted, nil
}

// AdmitCustomDomainTLSHost implements CustomDomainTLSHostStore. A wildcard
// whose app the MemStore does not hold is owned by its app ID, so narrow
// tests need not create one.
func (m *MemStore) AdmitCustomDomainTLSHost(_ context.Context, a CustomDomainTLSAdmission) (CustomDomainTLSVerdict, error) {
	a, err := normalizeTLSHostAdmission(a)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.customDomainTLSHosts == nil {
		m.customDomainTLSHosts = map[string]customDomainTLSHost{}
	}
	if prior, ok := m.customDomainTLSHosts[a.Host]; ok {
		if d, live := m.domains[prior.wildcardDomain]; live && d.Verified() {
			return TLSHostAdmitted, nil
		}
	}
	d, ok := m.domains[a.Wildcard]
	if !ok {
		return 0, ErrNotFound
	}
	if !d.Verified() {
		return TLSHostWildcardUnverified, nil
	}
	ownerID := d.AppID
	if app, ok := m.apps[d.AppID]; ok {
		ownerID = app.AccountID
		if app.OrgID != "" {
			ownerID = app.OrgID
		}
	}
	registered := tlsRegisteredDomain(a.Wildcard)
	cutoff := a.ledgerCutoff()
	kept := m.customDomainTLSAdmissions[:0]
	var fleet, perRegistered, perOwner int
	for _, r := range m.customDomainTLSAdmissions {
		if !r.admittedAt.After(cutoff) {
			continue
		}
		kept = append(kept, r)
		if r.admittedAt.After(a.Now.Add(-a.FleetWindow)) {
			fleet++
		}
		if r.admittedAt.After(a.Now.Add(-a.Window)) {
			if r.registeredDomain == registered {
				perRegistered++
			}
			if r.ownerID == ownerID {
				perOwner++
			}
		}
	}
	m.customDomainTLSAdmissions = kept
	if v := a.verdict(fleet, perRegistered, perOwner); v != TLSHostAdmitted {
		return v, nil
	}
	m.customDomainTLSHosts[a.Host] = customDomainTLSHost{wildcardDomain: a.Wildcard, admittedAt: a.Now}
	m.customDomainTLSAdmissions = append(m.customDomainTLSAdmissions, customDomainTLSAdmission{
		host: a.Host, registeredDomain: registered, ownerID: ownerID, admittedAt: a.Now,
	})
	return TLSHostAdmitted, nil
}

// dropCustomDomainTLSHostsLocked mirrors the ON DELETE CASCADE from
// custom_domains. The admission ledger has no foreign key and stays.
// Callers hold m.mu.
func (m *MemStore) dropCustomDomainTLSHostsLocked(domain string) {
	domain = strings.ToLower(domain)
	for host, h := range m.customDomainTLSHosts {
		if h.wildcardDomain == domain {
			delete(m.customDomainTLSHosts, host)
		}
	}
}
