package state

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainTLSKnownHostReader pages durable admission names into the edge's
// lookup hints after a restart. These names grant no certificate permission.
type CustomDomainTLSKnownHostReader interface {
	ListCustomDomainTLSKnownHosts(context.Context, string, int) ([]string, error)
}

// CustomDomainTLSRecentHostReader bounds catch-up to recent admissions and
// verified claims. These names are hints and never grant certificate permission.
type CustomDomainTLSRecentHostReader interface {
	ListRecentCustomDomainTLSHosts(context.Context, int) ([]string, error)
}

func (s *pgCore) ListRecentCustomDomainTLSHosts(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	hosts, err := sqlc.New().ListRecentCustomDomainTLSHosts(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list recent custom-domain TLS hosts: %w", err)
	}
	return hosts, nil
}

func (m *MemStore) ListRecentCustomDomainTLSHosts(_ context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := slices.Clone(m.customDomainTLSAdmissions)
	slices.SortFunc(rows, func(a, b customDomainTLSAdmission) int { return b.admittedAt.Compare(a.admittedAt) })
	seen := make(map[string]struct{})
	var hosts []string
	for _, row := range rows[:min(limit, len(rows))] {
		h, ok := m.customDomainTLSHosts[row.host]
		if !ok {
			continue
		}
		d, ok := m.domains[h.wildcardDomain]
		if !ok || !d.Verified() {
			continue
		}
		if _, ok := seen[row.host]; !ok {
			seen[row.host] = struct{}{}
			hosts = append(hosts, row.host)
		}
	}
	var verified []CustomDomain
	cutoff := time.Now().Add(-2 * time.Minute)
	for _, domain := range m.domains {
		if domain.Verified() && domain.VerifiedAt.After(cutoff) {
			verified = append(verified, domain)
		}
	}
	slices.SortFunc(verified, func(a, b CustomDomain) int { return b.VerifiedAt.Compare(a.VerifiedAt) })
	for _, domain := range verified[:min(limit, len(verified))] {
		if _, ok := seen[domain.Domain]; !ok {
			hosts = append(hosts, domain.Domain)
		}
	}
	return hosts, nil
}

func (s *pgCore) ListCustomDomainTLSKnownHosts(ctx context.Context, after string, limit int) ([]string, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	hosts, err := sqlc.New().ListCustomDomainTLSKnownHosts(ctx, s.pool, sqlc.ListCustomDomainTLSKnownHostsParams{AfterHost: after, PageSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list custom-domain TLS known hosts: %w", err)
	}
	return hosts, nil
}

func (m *MemStore) ListCustomDomainTLSKnownHosts(_ context.Context, after string, limit int) ([]string, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var hosts []string
	for host := range m.customDomainTLSHosts {
		if host > after {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	return hosts[:min(limit, len(hosts))], nil
}

var (
	_ CustomDomainTLSKnownHostReader  = (*EdgeStore)(nil)
	_ CustomDomainTLSKnownHostReader  = (*MemStore)(nil)
	_ CustomDomainTLSRecentHostReader = (*EdgeStore)(nil)
	_ CustomDomainTLSRecentHostReader = (*MemStore)(nil)
)
