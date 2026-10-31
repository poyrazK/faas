package state

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// CustomDomainTLSAllowedHostReader is the public edge's read of a wildcard
// custom domain's optional host allowlist (ADR-520 amendment 2). It is an
// optional seam outside Store so narrow test doubles stay source-compatible.
type CustomDomainTLSAllowedHostReader interface {
	// CustomDomainTLSHostAllowed reports whether host, below the wildcard
	// custom domain wildcardDomain, may hold an on-demand certificate: the
	// wildcard has no allowlist, the allowlist names host, or host is the
	// wildcard's probe host (WildcardProbeHost), which the domain doctor
	// dials.
	CustomDomainTLSHostAllowed(ctx context.Context, wildcardDomain, host string) (bool, error)
}

// CustomDomainTLSAllowedHostStore is apid's read and write of the allowlist.
// apid is its only writer: it is customer intent.
type CustomDomainTLSAllowedHostStore interface {
	// CustomDomainTLSAllowedHosts returns the wildcard's allowlisted names,
	// sorted; empty means every name below it is allowed. A missing
	// wildcard returns ErrNotFound.
	CustomDomainTLSAllowedHosts(ctx context.Context, wildcardDomain string) ([]string, error)
	// SetCustomDomainTLSAllowedHosts replaces the allowlist with names,
	// which it normalizes (NormalizeWildcardHostNames); an empty list
	// clears it. A missing wildcard returns ErrNotFound.
	SetCustomDomainTLSAllowedHosts(ctx context.Context, wildcardDomain string, names []string) error
}

var wildcardHostNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// WildcardHostName returns the part of host before wildcardDomain's suffix
// ("eu.shop" for eu.shop.example.com under *.example.com), the form the
// allowlist stores.
func WildcardHostName(wildcardDomain, host string) (string, bool) {
	suffix, ok := WildcardDomainSuffix(wildcardDomain)
	if !ok || !WildcardMatchesHost(wildcardDomain, host) {
		return "", false
	}
	return strings.TrimSuffix(normalizeWildcardDomainHost(host), "."+suffix), true
}

// NormalizeWildcardHostNames lowercases, validates, de-duplicates and sorts
// allowlist names for wildcardDomain. A name may also be given as the full
// host. Each name, joined to the wildcard's suffix, must be a valid custom
// domain name, and at most api.CustomDomainTLSAllowedHostsMax are accepted.
func NormalizeWildcardHostNames(wildcardDomain string, names []string) ([]string, error) {
	suffix, ok := WildcardDomainSuffix(wildcardDomain)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a wildcard custom domain", ErrInvalidArgument, wildcardDomain)
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "."+suffix)
		if !wildcardHostNameRE.MatchString(name) || ValidateCustomDomainName(name+"."+suffix) != nil {
			return nil, fmt.Errorf("%w: %q is not a name below %s", ErrInvalidArgument, name, wildcardDomain)
		}
		out = append(out, name)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > api.CustomDomainTLSAllowedHostsMax {
		return nil, fmt.Errorf("%w: %d names, at most %d", ErrInvalidArgument, len(out), api.CustomDomainTLSAllowedHostsMax)
	}
	return out, nil
}

// probeHostAllowed reports whether host is the wildcard's doctor probe host,
// which every allowlist admits so verification and drift checks keep working.
func probeHostAllowed(wildcardDomain, host string) bool {
	probe, ok := WildcardProbeHost(wildcardDomain)
	return ok && normalizeWildcardDomainHost(host) == probe
}

// CustomDomainTLSHostAllowed implements CustomDomainTLSAllowedHostReader.
func (s *pgCore) CustomDomainTLSHostAllowed(ctx context.Context, wildcardDomain, host string) (bool, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	name, ok := WildcardHostName(wildcardDomain, host)
	if !ok {
		return false, fmt.Errorf("%w: %q is not below %q", ErrInvalidArgument, host, wildcardDomain)
	}
	if probeHostAllowed(wildcardDomain, host) {
		return true, nil
	}
	var allowed bool
	if err := s.pool.QueryRow(ctx,
		`select count(*) = 0 or coalesce(bool_or(name = $2), false)
		   from custom_domain_tls_allowed_hosts where wildcard_domain = $1`,
		wildcardDomain, name).Scan(&allowed); err != nil {
		return false, fmt.Errorf("custom-domain tls allowlist: %w", mapErr(err))
	}
	return allowed, nil
}

// CustomDomainTLSAllowedHosts implements CustomDomainTLSAllowedHostStore.
func (s *pgCore) CustomDomainTLSAllowedHosts(ctx context.Context, wildcardDomain string) ([]string, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	var names []string
	if err := s.pool.QueryRow(ctx,
		`select coalesce(array_agg(h.name::text order by h.name) filter (where h.name is not null), '{}')
		   from custom_domains d
		   left join custom_domain_tls_allowed_hosts h on h.wildcard_domain = d.domain
		  where d.domain = $1
		  group by d.domain`,
		wildcardDomain).Scan(&names); err != nil {
		return nil, fmt.Errorf("custom-domain tls allowlist: %w", mapErr(err))
	}
	return names, nil
}

// SetCustomDomainTLSAllowedHosts implements CustomDomainTLSAllowedHostStore.
// It is on PgStore, not pgCore, so the edge's EdgeStore cannot write it.
func (s *PgStore) SetCustomDomainTLSAllowedHosts(ctx context.Context, wildcardDomain string, names []string) error {
	wildcardDomain = strings.ToLower(wildcardDomain)
	names, err := NormalizeWildcardHostNames(wildcardDomain, names)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("set custom-domain tls allowlist: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked string
	if err := tx.QueryRow(ctx,
		`select domain::text from custom_domains where domain = $1 for no key update`, wildcardDomain).Scan(&locked); err != nil {
		return fmt.Errorf("set custom-domain tls allowlist: lock wildcard: %w", mapErr(err))
	}
	if _, err := tx.Exec(ctx,
		`delete from custom_domain_tls_allowed_hosts where wildcard_domain = $1`, wildcardDomain); err != nil {
		return fmt.Errorf("set custom-domain tls allowlist: clear: %w", mapErr(err))
	}
	if len(names) > 0 {
		if _, err := tx.Exec(ctx,
			`insert into custom_domain_tls_allowed_hosts (wildcard_domain, name)
			 select $1, unnest($2::text[])`, wildcardDomain, names); err != nil {
			return fmt.Errorf("set custom-domain tls allowlist: insert: %w", mapErr(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set custom-domain tls allowlist: commit: %w", err)
	}
	return nil
}

// CustomDomainTLSHostAllowed implements CustomDomainTLSAllowedHostReader.
func (m *MemStore) CustomDomainTLSHostAllowed(_ context.Context, wildcardDomain, host string) (bool, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	name, ok := WildcardHostName(wildcardDomain, host)
	if !ok {
		return false, fmt.Errorf("%w: %q is not below %q", ErrInvalidArgument, host, wildcardDomain)
	}
	if probeHostAllowed(wildcardDomain, host) {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	names := m.customDomainTLSAllowedHosts[wildcardDomain]
	return len(names) == 0 || slices.Contains(names, name), nil
}

// CustomDomainTLSAllowedHosts implements CustomDomainTLSAllowedHostStore.
func (m *MemStore) CustomDomainTLSAllowedHosts(_ context.Context, wildcardDomain string) ([]string, error) {
	wildcardDomain = strings.ToLower(wildcardDomain)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[wildcardDomain]; !ok {
		return nil, ErrNotFound
	}
	return append([]string{}, m.customDomainTLSAllowedHosts[wildcardDomain]...), nil
}

// SetCustomDomainTLSAllowedHosts implements CustomDomainTLSAllowedHostStore.
func (m *MemStore) SetCustomDomainTLSAllowedHosts(_ context.Context, wildcardDomain string, names []string) error {
	wildcardDomain = strings.ToLower(wildcardDomain)
	names, err := NormalizeWildcardHostNames(wildcardDomain, names)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[wildcardDomain]; !ok {
		return ErrNotFound
	}
	if m.customDomainTLSAllowedHosts == nil {
		m.customDomainTLSAllowedHosts = map[string][]string{}
	}
	if len(names) == 0 {
		delete(m.customDomainTLSAllowedHosts, wildcardDomain)
		return nil
	}
	m.customDomainTLSAllowedHosts[wildcardDomain] = names
	return nil
}

var (
	_ CustomDomainTLSAllowedHostReader = (*EdgeStore)(nil)
	_ CustomDomainTLSAllowedHostStore  = (*PgStore)(nil)
	_ CustomDomainTLSAllowedHostStore  = (*MemStore)(nil)
)
