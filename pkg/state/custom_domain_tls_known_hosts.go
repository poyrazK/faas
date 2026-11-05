package state

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainTLSKnownHostReader pages durable admission names into the edge's
// lookup hints after a restart. These names grant no certificate permission.
type CustomDomainTLSKnownHostReader interface {
	ListCustomDomainTLSKnownHosts(context.Context, string, int) ([]string, error)
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
	_ CustomDomainTLSKnownHostReader = (*EdgeStore)(nil)
	_ CustomDomainTLSKnownHostReader = (*MemStore)(nil)
)
