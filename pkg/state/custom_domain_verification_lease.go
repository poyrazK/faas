package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainVerificationLeaseStore releases checks abandoned by a bounded
// verification pass. Unattempted claims must not accumulate retry backoff.
type CustomDomainVerificationLeaseStore interface {
	ReleaseCustomDomainVerificationLeases(context.Context, []CustomDomain) error
}

type customDomainVerificationLease struct {
	Domain    string    `json:"domain"`
	AppID     string    `json:"app_id"`
	Token     string    `json:"token"`
	Attempts  int       `json:"attempts"`
	NextCheck time.Time `json:"next_check_at"`
}

func (s *PgStore) ReleaseCustomDomainVerificationLeases(ctx context.Context, claims []CustomDomain) error {
	leases := make([]customDomainVerificationLease, 0, len(claims))
	for _, d := range claims {
		if d.VerificationAttempts > 0 {
			leases = append(leases, customDomainVerificationLease{d.Domain, d.AppID, d.ChallengeToken, d.VerificationAttempts, d.VerificationNextCheckAt})
		}
	}
	if len(leases) == 0 {
		return nil
	}
	data, err := json.Marshal(leases)
	if err != nil {
		return fmt.Errorf("encode domain verification leases: %w", err)
	}
	if err := sqlc.New().ReleaseCustomDomainVerificationLeases(ctx, s.pool, data); err != nil {
		return fmt.Errorf("release domain verification leases: %w", err)
	}
	return nil
}

func (m *MemStore) ReleaseCustomDomainVerificationLeases(_ context.Context, claims []CustomDomain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, claim := range claims {
		d, ok := m.domains[claim.Domain]
		if !ok || d.Verified() || !SameCustomDomainClaim(claim, d) || claim.VerificationAttempts <= 0 ||
			d.VerificationAttempts != claim.VerificationAttempts || !d.VerificationNextCheckAt.Equal(claim.VerificationNextCheckAt) {
			continue
		}
		d.VerificationAttempts--
		d.VerificationNextCheckAt = time.Now()
		m.domains[d.Domain] = d
	}
	return nil
}
