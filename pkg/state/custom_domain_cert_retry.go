package state

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CustomDomainCertificateRetryReader discovers verified wildcard claims whose
// certificate needs issuance or renewal. Discovery grants no permission.
type CustomDomainCertificateRetryReader interface {
	ListCustomDomainCertificateRetries(context.Context, time.Time, time.Time, int) ([]CustomDomain, error)
}

// CustomDomainCertificateAttemptStore serializes eager and retry attempts for
// the captured claim, including across gateway processes and restarts.
type CustomDomainCertificateAttemptStore interface {
	BeginCustomDomainCertificateAttempt(context.Context, CustomDomain, time.Time, time.Duration) (bool, error)
}

type customDomainCertRetryLease struct {
	claim CustomDomain
	until time.Time
}

func (s *PgStore) ListCustomDomainCertificateRetries(ctx context.Context, now, renewBefore time.Time, limit int) ([]CustomDomain, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListCustomDomainCertificateRetries(ctx, s.pool, sqlc.ListCustomDomainCertificateRetriesParams{
		Now: pgtypeFromTime(now), RenewBefore: pgtypeFromTime(renewBefore), BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list custom-domain certificate retries: %w", err)
	}
	out := make([]CustomDomain, 0, len(rows))
	for _, row := range rows {
		out = append(out, CustomDomain{Domain: row.Domain, AppID: row.AppID, ChallengeToken: row.Token})
	}
	return out, nil
}

func (s *PgStore) BeginCustomDomainCertificateAttempt(ctx context.Context, claim CustomDomain, now time.Time, delay time.Duration) (bool, error) {
	if delay <= 0 || !IsWildcardCustomDomain(claim.Domain) {
		return false, ErrInvalidArgument
	}
	n, err := sqlc.New().BeginCustomDomainCertificateAttempt(ctx, s.pool, sqlc.BeginCustomDomainCertificateAttemptParams{
		Domain: claim.Domain, AppID: claim.AppID, Token: claim.ChallengeToken,
		Now: pgtypeFromTime(now), RetryAt: pgtypeFromTime(now.Add(delay)),
	})
	if err != nil {
		return false, fmt.Errorf("begin custom-domain certificate attempt: %w", err)
	}
	return n != 0, nil
}

func (m *MemStore) ListCustomDomainCertificateRetries(_ context.Context, now, renewBefore time.Time, limit int) ([]CustomDomain, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		if !d.Verified() || !IsWildcardCustomDomain(d.Domain) || !customDomainCertificateNeedsRetry(d, renewBefore) {
			continue
		}
		if lease := m.customDomainCertRetries[d.Domain]; SameCustomDomainClaim(lease.claim, d) && lease.until.After(now) {
			continue
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b CustomDomain) int {
		if cmp := m.customDomainCertRetries[a.Domain].until.Compare(m.customDomainCertRetries[b.Domain].until); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Domain, b.Domain)
	})
	return out[:min(len(out), limit)], nil
}

func customDomainCertificateNeedsRetry(d CustomDomain, renewBefore time.Time) bool {
	return d.CertStatus == CustomDomainCertPending || d.CertStatus == CustomDomainCertFailed ||
		(d.CertStatus == CustomDomainCertIssued || d.CertStatus == CustomDomainCertRenewing) &&
			(d.CertExpiresAt.IsZero() || !d.CertExpiresAt.After(renewBefore))
}

func (m *MemStore) BeginCustomDomainCertificateAttempt(_ context.Context, claim CustomDomain, now time.Time, delay time.Duration) (bool, error) {
	if delay <= 0 || !IsWildcardCustomDomain(claim.Domain) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[claim.Domain]
	if !ok || !d.Verified() || !SameCustomDomainClaim(claim, d) {
		return false, nil
	}
	if lease := m.customDomainCertRetries[claim.Domain]; SameCustomDomainClaim(lease.claim, claim) && lease.until.After(now) {
		return false, nil
	}
	if m.customDomainCertRetries == nil {
		m.customDomainCertRetries = make(map[string]customDomainCertRetryLease)
	}
	m.customDomainCertRetries[claim.Domain] = customDomainCertRetryLease{claim: claim, until: now.Add(delay)}
	return true, nil
}
