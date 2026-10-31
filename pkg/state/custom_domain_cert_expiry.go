package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainCertExpiry is the expiry of the edge certificate the domain
// doctor last observed for a verified custom domain.
type CustomDomainCertExpiry struct {
	Domain   string
	NotAfter time.Time
}

// CustomDomainCertExpiryStore reads the certificate expiries the domain doctor
// recorded. The certificates live in the edge's Caddy storage, which no Go
// daemon reads, so these are the source of the certificate-expiry alerts
// (bug hunt 32 M6).
type CustomDomainCertExpiryStore interface {
	SoonestCustomDomainCertExpiries(ctx context.Context, limit int) ([]CustomDomainCertExpiry, error)
}

// SoonestCustomDomainCertExpiries returns up to limit verified domains with an
// observed certificate, soonest expiry first.
func (s *PgStore) SoonestCustomDomainCertExpiries(ctx context.Context, limit int) ([]CustomDomainCertExpiry, error) {
	rows, err := sqlc.New().ListSoonestExpiringCustomDomainCerts(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list custom domain certificate expiries: %w", err)
	}
	out := make([]CustomDomainCertExpiry, 0, len(rows))
	for _, row := range rows {
		out = append(out, CustomDomainCertExpiry{Domain: row.Domain, NotAfter: row.CertExpiresAt.Time.UTC()})
	}
	return out, nil
}

// SoonestCustomDomainCertExpiries mirrors the Postgres query.
func (m *MemStore) SoonestCustomDomainCertExpiries(_ context.Context, limit int) ([]CustomDomainCertExpiry, error) {
	m.mu.Lock()
	out := make([]CustomDomainCertExpiry, 0, len(m.domains))
	for _, d := range m.domains {
		if d.Verified() && !d.CertExpiresAt.IsZero() {
			out = append(out, CustomDomainCertExpiry{Domain: d.Domain, NotAfter: d.CertExpiresAt.UTC()})
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NotAfter.Equal(out[j].NotAfter) {
			return out[i].NotAfter.Before(out[j].NotAfter)
		}
		return out[i].Domain < out[j].Domain
	})
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ CustomDomainCertExpiryStore = (*PgStore)(nil)
var _ CustomDomainCertExpiryStore = (*MemStore)(nil)
