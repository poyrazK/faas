package state

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainInventoryStore reads candidate domains for an actor, including
// company apps created by other members. Callers must still apply current app
// access and credential scopes. Accounting inventories remain creator-owned.
type CustomDomainInventoryStore interface {
	ListDomainsForActor(ctx context.Context, accountID string) ([]CustomDomain, error)
}

func (s *PgStore) ListDomainsForActor(ctx context.Context, accountID string) ([]CustomDomain, error) {
	rows, err := sqlc.New().ListCustomDomainsForActor(ctx, s.pool, mustPgUUID(accountID))
	if err != nil {
		return nil, fmt.Errorf("list custom domains for actor: %w", err)
	}
	out := make([]CustomDomain, 0, len(rows))
	for _, row := range rows {
		out = append(out, CustomDomain{
			Domain: row.Domain, AppID: row.AppID, ChallengeToken: row.ChallengeToken,
			VerifiedAt: row.VerifiedAt.Time, CertStatus: CustomDomainCertStatus(row.CertStatus),
			CertExpiresAt: row.CertExpiresAt.Time, CertLastError: row.CertLastError,
			DNSLastCheckedAt: row.DnsLastCheckedAt.Time, CertFailedAt: row.CertFailedAt.Time,
			VerificationNextCheckAt: row.VerificationNextCheckAt.Time,
			VerificationExpiresAt:   row.VerificationExpiresAt.Time,
			VerificationAttempts:    int(row.VerificationAttempts), EnvironmentID: row.EnvironmentID,
		})
	}
	return out, nil
}

func (m *MemStore) ListDomainsForActor(_ context.Context, accountID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orgs := make(map[string]bool)
	for key, member := range m.memberships {
		if key.AccountID == accountID && member.RemovedAt == nil {
			orgs[key.OrgID] = true
		}
	}
	var out []CustomDomain
	for _, d := range m.domains {
		if app, ok := m.apps[d.AppID]; ok && (app.AccountID == accountID || orgs[app.OrgID]) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

var (
	_ CustomDomainInventoryStore = (*PgStore)(nil)
	_ CustomDomainInventoryStore = (*MemStore)(nil)
)
