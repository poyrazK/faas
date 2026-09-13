package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrCustomDomainQuotaExceeded = errors.New("state: custom domain quota exceeded")

type CustomDomainQuotaError struct {
	Scope string
	Limit int
}

func (e *CustomDomainQuotaError) Error() string {
	return fmt.Sprintf("%v (%s limit=%d)", ErrCustomDomainQuotaExceeded, e.Scope, e.Limit)
}
func (e *CustomDomainQuotaError) Unwrap() error { return ErrCustomDomainQuotaExceeded }

func (s *PgStore) CreateCustomDomainIfUnderQuota(ctx context.Context, domain, appID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CustomDomain{}, err
	}
	defer tx.Rollback(ctx)
	var accountID string
	if err = tx.QueryRow(ctx, `select account_id from apps where id=$1 and status <> 'deleted' for update`, appID).Scan(&accountID); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	var n int
	if err = tx.QueryRow(ctx, `select count(*) from custom_domains where app_id=$1 and verified_at is null and verification_expires_at > now()`, appID).Scan(&n); err != nil {
		return CustomDomain{}, err
	}
	if n >= appLimit {
		return CustomDomain{}, &CustomDomainQuotaError{"app", appLimit}
	}
	// The account row serializes creates made concurrently for different apps.
	if err = tx.QueryRow(ctx, `select 1 from accounts where id=$1 for update`, accountID).Scan(&n); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	if err = tx.QueryRow(ctx, `select count(*) from custom_domains d join apps a on a.id=d.app_id where a.account_id=$1 and d.verified_at is null and d.verification_expires_at > now()`, accountID).Scan(&n); err != nil {
		return CustomDomain{}, err
	}
	if n >= accountLimit {
		return CustomDomain{}, &CustomDomainQuotaError{"account", accountLimit}
	}
	row := tx.QueryRow(ctx, `insert into custom_domains(domain,app_id,challenge_token) values($1,$2,$3) returning domain,app_id,challenge_token,coalesce(verified_at,'epoch'),cert_status,coalesce(cert_expires_at,'epoch'),coalesce(cert_last_error,''),coalesce(dns_last_checked_at,'epoch'),coalesce(cert_failed_at,'epoch')`, domain, appID, token)
	var d CustomDomain
	if err = scanCustomDomain(row, &d); err != nil {
		return d, mapErr(err)
	}
	return d, tx.Commit(ctx)
}

func (s *PgStore) ClaimCustomDomainsForVerification(ctx context.Context, limit int) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx, `with due as (select d.domain from custom_domains d where d.verified_at is null and d.verification_next_check_at<=now() and d.verification_expires_at>now() order by row_number() over(partition by d.app_id order by d.verification_next_check_at,d.domain),d.verification_next_check_at limit $1 for update skip locked), bumped as (update custom_domains d set verification_attempts=d.verification_attempts+1, verification_next_check_at=now()+least(interval '1 hour',interval '30 seconds'*power(2,least(d.verification_attempts,7))) from due where d.domain=due.domain returning d.*) select domain,app_id,challenge_token,coalesce(verified_at,'epoch'),cert_status,coalesce(cert_expires_at,'epoch'),coalesce(cert_last_error,''),coalesce(dns_last_checked_at,'epoch'),coalesce(cert_failed_at,'epoch') from bumped`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomains(rows)
}
func (s *PgStore) RetryCustomDomainVerification(ctx context.Context, domain string) error {
	tag, err := s.pool.Exec(ctx, `update custom_domains set verification_next_check_at=now(),verification_expires_at=now()+interval '7 days',verification_attempts=0 where domain=$1 and verified_at is null`, domain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) CreateCustomDomainIfUnderQuota(_ context.Context, domain, appID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return CustomDomain{}, ErrNotFound
	}
	ac, ap := 0, 0
	now := time.Now()
	for _, d := range m.domains {
		if d.Verified() || (!d.VerificationExpiresAt.IsZero() && !d.VerificationExpiresAt.After(now)) {
			continue
		}
		if d.AppID == appID {
			ap++
		}
		if x := m.apps[d.AppID]; x.AccountID == a.AccountID {
			ac++
		}
	}
	if ap >= appLimit {
		return CustomDomain{}, &CustomDomainQuotaError{"app", appLimit}
	}
	if ac >= accountLimit {
		return CustomDomain{}, &CustomDomainQuotaError{"account", accountLimit}
	}
	d := CustomDomain{Domain: domain, AppID: appID, ChallengeToken: token, CertStatus: CustomDomainCertPending, VerificationNextCheckAt: now, VerificationExpiresAt: now.Add(7 * 24 * time.Hour)}
	m.domains[domain] = d
	return d, nil
}
func (m *MemStore) ClaimCustomDomainsForVerification(_ context.Context, limit int) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	seen := map[string]bool{}
	out := []CustomDomain{}
	for k, d := range m.domains {
		if len(out) >= limit {
			break
		}
		a := m.apps[d.AppID].AccountID
		if (!d.VerificationExpiresAt.IsZero() && seen[a]) || d.Verified() || d.VerificationNextCheckAt.After(now) || (!d.VerificationExpiresAt.IsZero() && !d.VerificationExpiresAt.After(now)) {
			continue
		}
		if !d.VerificationExpiresAt.IsZero() {
			seen[a] = true
		}
		if !d.VerificationExpiresAt.IsZero() {
			d.VerificationAttempts++
			delay := 30 * time.Second * time.Duration(1<<min(d.VerificationAttempts-1, 7))
			if delay > time.Hour {
				delay = time.Hour
			}
			d.VerificationNextCheckAt = now.Add(delay)
		}
		m.domains[k] = d
		out = append(out, d)
	}
	return out, nil
}
func (m *MemStore) RetryCustomDomainVerification(_ context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok || d.Verified() {
		return ErrNotFound
	}
	d.VerificationAttempts = 0
	d.VerificationNextCheckAt = time.Now()
	d.VerificationExpiresAt = time.Now().Add(7 * 24 * time.Hour)
	m.domains[domain] = d
	return nil
}
