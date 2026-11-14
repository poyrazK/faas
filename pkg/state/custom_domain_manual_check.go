package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainManualCheckStore reserves manual probes across API processes.
// A claim replacement cannot inherit an old probe's observations or lease.
type CustomDomainManualCheckStore interface {
	BeginCustomDomainManualCheck(context.Context, CustomDomain, time.Time, time.Duration) (bool, error)
	CustomDomainCertificateRetryAt(context.Context, CustomDomain) (time.Time, error)
}

func (s *PgStore) BeginCustomDomainManualCheck(ctx context.Context, d CustomDomain, now time.Time, cooldown time.Duration) (bool, error) {
	if cooldown <= 0 {
		return false, ErrInvalidArgument
	}
	n, err := sqlc.New().BeginCustomDomainManualCheck(ctx, s.pool, sqlc.BeginCustomDomainManualCheckParams{
		Domain: d.Domain, AppID: d.AppID, Token: d.ChallengeToken, CheckedAt: pgtypeFromTime(now), DueBefore: pgtypeFromTime(now.Add(-cooldown)),
	})
	if err != nil {
		return false, fmt.Errorf("reserve manual domain check: %w", err)
	}
	return n != 0, nil
}

func (s *PgStore) CustomDomainCertificateRetryAt(ctx context.Context, d CustomDomain) (time.Time, error) {
	schedule, err := sqlc.New().ReadCustomDomainCheckSchedule(ctx, s.pool, sqlc.ReadCustomDomainCheckScheduleParams{Domain: d.Domain, AppID: d.AppID, Token: d.ChallengeToken})
	if err != nil {
		return time.Time{}, fmt.Errorf("read domain check schedule: %w", mapErr(err))
	}
	return schedule.CertRetryAt.Time, nil
}

func (m *MemStore) BeginCustomDomainManualCheck(_ context.Context, claim CustomDomain, now time.Time, cooldown time.Duration) (bool, error) {
	if cooldown <= 0 {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[claim.Domain]
	if !ok || !SameCustomDomainClaim(claim, d) || !d.ClaimLive(now) {
		return false, nil
	}
	if !d.VerificationManualCheckAt.IsZero() && d.VerificationManualCheckAt.After(now.Add(-cooldown)) {
		return false, nil
	}
	d.VerificationManualCheckAt = now
	m.domains[d.Domain] = d
	return true, nil
}

func (m *MemStore) CustomDomainCertificateRetryAt(_ context.Context, claim CustomDomain) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[claim.Domain]
	if !ok || !SameCustomDomainClaim(claim, d) {
		return time.Time{}, ErrNotFound
	}
	lease := m.customDomainCertRetries[claim.Domain]
	if !SameCustomDomainClaim(lease.claim, d) {
		return time.Time{}, nil
	}
	return lease.until, nil
}
