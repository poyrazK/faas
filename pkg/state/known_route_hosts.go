package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// KnownRouteHostStore lists app slugs, verified custom-domain claims and
// admitted concrete wildcard hosts, at most limit of each. Exact names may
// skip the new-host budget; wildcard patterns qualify only for a separately
// bounded recovery budget. Routing still reads and checks the live row.
type KnownRouteHostStore interface {
	KnownRouteHosts(ctx context.Context, limit int) (slugs, domains []string, err error)
}

var (
	_ KnownRouteHostStore = (*PgStore)(nil)
	_ KnownRouteHostStore = (*EdgeStore)(nil)
	_ KnownRouteHostStore = (*MemStore)(nil)
)

// KnownRouteHosts implements KnownRouteHostStore.
func (s *pgCore) KnownRouteHosts(ctx context.Context, limit int) ([]string, []string, error) {
	q := sqlc.New()
	n := int32(min(max(limit, 0), 1<<31-1))
	slugs, err := q.KnownRouteAppSlugs(ctx, s.pool, n)
	if err != nil {
		return nil, nil, fmt.Errorf("state: list known app slugs: %w", mapErr(err))
	}
	domains, err := q.KnownRouteCustomDomains(ctx, s.pool, n)
	if err != nil {
		return nil, nil, fmt.Errorf("state: list known custom domains: %w", mapErr(err))
	}
	return slugs, domains, nil
}

// KnownRouteHosts implements KnownRouteHostStore.
func (m *MemStore) KnownRouteHosts(_ context.Context, limit int) ([]string, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var slugs, domains []string
	for _, app := range m.apps {
		if len(slugs) < limit {
			slugs = append(slugs, app.Slug)
		}
	}
	for name, d := range m.domains {
		if len(domains) < limit && d.Verified() {
			domains = append(domains, strings.ToLower(name))
		}
	}
	for host, admission := range m.customDomainTLSHosts {
		if len(domains) >= limit {
			break
		}
		if d, ok := m.domains[admission.wildcardDomain]; ok && d.Verified() && IsWildcardCustomDomain(d.Domain) {
			domains = append(domains, strings.ToLower(host))
		}
	}
	return slugs, domains, nil
}
