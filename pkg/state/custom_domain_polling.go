package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrCustomDomainQuotaExceeded = errors.New("state: custom domain quota exceeded")

// CustomDomainChallengeVerifier atomically verifies only the claim whose TXT
// token was observed. It prevents a slow DNS lookup for an expired claim from
// verifying a replacement claim created by another account.
type CustomDomainChallengeVerifier interface {
	MarkDomainVerifiedIfChallenge(ctx context.Context, domain, token string) (bool, error)
}

type CustomDomainQuotaError struct {
	Scope string
	Limit int
}

func (e *CustomDomainQuotaError) Error() string {
	return fmt.Sprintf("%v (%s limit=%d)", ErrCustomDomainQuotaExceeded, e.Scope, e.Limit)
}
func (e *CustomDomainQuotaError) Unwrap() error { return ErrCustomDomainQuotaExceeded }

func (s *PgStore) CreateCustomDomainIfUnderQuota(ctx context.Context, domain, appID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	d, _, err := s.createCustomDomainIfUnderQuota(ctx, domain, appID, "", token, appLimit, accountLimit, nil)
	return d, err
}

// CreateCustomDomainInEnvironmentIfUnderQuota claims a custom hostname for a
// workload in one environment. It rejects app/environment pairs from
// different projects inside the same transaction that claims the domain.
func (s *PgStore) CreateCustomDomainInEnvironmentIfUnderQuota(ctx context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	if environmentID == "" {
		return CustomDomain{}, ErrInvalidArgument
	}
	d, _, err := s.createCustomDomainIfUnderQuota(ctx, domain, appID, environmentID, token, appLimit, accountLimit, nil)
	return d, err
}

func (s *PgStore) CreateCustomDomainIfUnderQuotaWithActivity(ctx context.Context, domain, appID, token string, appLimit, accountLimit int, entry OrgActivity) (CustomDomain, int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return CustomDomain{}, 0, err
	}
	return s.createCustomDomainIfUnderQuota(ctx, domain, appID, "", token, appLimit, accountLimit, &entry)
}

func (s *PgStore) CreateCustomDomainInEnvironmentIfUnderQuotaWithActivity(ctx context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int, entry OrgActivity) (CustomDomain, int64, error) {
	if environmentID == "" {
		return CustomDomain{}, 0, ErrInvalidArgument
	}
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return CustomDomain{}, 0, err
	}
	return s.createCustomDomainIfUnderQuota(ctx, domain, appID, environmentID, token, appLimit, accountLimit, &entry)
}

func (s *PgStore) createCustomDomainIfUnderQuota(ctx context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int, entry *OrgActivity) (CustomDomain, int64, error) {
	account, err := sqlc.New().ReadAppTrafficAccount(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return CustomDomain{}, 0, mapErr(err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CustomDomain{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match traffic publication's account-before-app lock order. Domain
	// claims themselves are unverified and introduce no serving URL.
	if _, err := sqlc.New().LockCustomDomainQuotaAccount(ctx, tx, account); err != nil {
		return CustomDomain{}, 0, mapErr(err)
	}
	var accountID string
	if err = tx.QueryRow(ctx, `select a.account_id from apps a
		left join project_environments e on e.id = nullif($2, '')::uuid
		where a.id=$1 and a.status <> 'deleted'
		  and ($2 = '' or (a.project_id = e.project_id and a.account_id = e.account_id))
		for update of a`, appID, environmentID).Scan(&accountID); err != nil {
		return CustomDomain{}, 0, mapErr(err)
	}
	var n int
	if err = tx.QueryRow(ctx, `select count(*) from custom_domains where app_id=$1 and verified_at is null and verification_expires_at > now()`, appID).Scan(&n); err != nil {
		return CustomDomain{}, 0, err
	}
	if n >= appLimit {
		return CustomDomain{}, 0, &CustomDomainQuotaError{"app", appLimit}
	}
	// The account row serializes creates made concurrently for different apps.
	if err = tx.QueryRow(ctx, `select count(*) from custom_domains d join apps a on a.id=d.app_id where a.account_id=$1 and d.verified_at is null and d.verification_expires_at > now()`, accountID).Scan(&n); err != nil {
		return CustomDomain{}, 0, err
	}
	if n >= accountLimit {
		return CustomDomain{}, 0, &CustomDomainQuotaError{"account", accountLimit}
	}
	// A pending claim is deliberately exclusive only until its verification
	// deadline. ON CONFLICT performs the handoff under the domain primary-key
	// lock, so two accounts racing to reclaim an expired claim cannot both win.
	// Verified and still-active pending rows fail closed and remain untouched.
	row := tx.QueryRow(ctx, `
		insert into custom_domains(domain,app_id,challenge_token,environment_id)
		values($1,$2,$3,nullif($4,'')::uuid)
		on conflict (domain) do update
		set app_id = excluded.app_id,
		    environment_id = excluded.environment_id,
		    app_id_redirect = null,
		    challenge_token = excluded.challenge_token,
		    verified_at = null,
		    cert_status = 'pending',
		    cert_expires_at = null,
		    cert_last_error = null,
		    dns_last_checked_at = null,
		    cert_failed_at = null,
		    last_cert_issuance_failed_email_at = null,
		    verification_next_check_at = now(),
		    verification_attempts = 0,
		    verification_expires_at = now() + interval '7 days'
		where custom_domains.verified_at is null
		  and custom_domains.verification_expires_at <= now()
		returning domain,app_id,challenge_token,coalesce(verified_at,'epoch'),
		          cert_status,coalesce(cert_expires_at,'epoch'),
		          coalesce(cert_last_error,''),coalesce(dns_last_checked_at,'epoch'),
		          coalesce(cert_failed_at,'epoch'),verification_next_check_at,
		          verification_expires_at,verification_attempts,coalesce(environment_id::text,'')`, domain, appID, token, environmentID)
	var d CustomDomain
	if err = scanCustomDomain(row, &d); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return d, 0, ErrConflict
		}
		return d, 0, mapErr(err)
	}
	var outboxID int64
	if entry != nil {
		outboxID, err = enqueueOrgActivityOutboxTx(ctx, tx, *entry)
		if err != nil {
			return CustomDomain{}, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomDomain{}, 0, err
	}
	return d, outboxID, nil
}

func (s *PgStore) ClaimCustomDomainsForVerification(ctx context.Context, limit int) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx, `with accounts_due as (select a.account_id,min(d.verification_next_check_at) oldest from custom_domains d join apps a on a.id=d.app_id where d.verified_at is null and d.verification_next_check_at<=now() and d.verification_expires_at>now() group by a.account_id order by oldest limit $1), due as (select candidate.domain from accounts_due q cross join lateral (select d.domain from custom_domains d join apps a on a.id=d.app_id where a.account_id=q.account_id and d.verified_at is null and d.verification_next_check_at<=now() and d.verification_expires_at>now() order by d.verification_next_check_at,d.domain limit 1 for update of d skip locked) candidate), bumped as (update custom_domains d set verification_attempts=d.verification_attempts+1, verification_next_check_at=now()+least(interval '1 hour',interval '30 seconds'*power(2,least(d.verification_attempts,7))) from due where d.domain=due.domain returning d.*) select domain,app_id,challenge_token,coalesce(verified_at,'epoch'),cert_status,coalesce(cert_expires_at,'epoch'),coalesce(cert_last_error,''),coalesce(dns_last_checked_at,'epoch'),coalesce(cert_failed_at,'epoch'),verification_next_check_at,verification_expires_at,verification_attempts,coalesce(environment_id::text,'') from bumped`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomains(rows)
}

func (s *PgStore) CustomDomainVerificationStats(ctx context.Context) (int, time.Duration, error) {
	var n int
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `select count(*),min(verification_next_check_at) from custom_domains where verified_at is null and verification_expires_at>now()`).Scan(&n, &oldest)
	if err != nil {
		return 0, 0, err
	}
	if oldest == nil {
		return n, 0, nil
	}
	age := time.Since(*oldest)
	if age < 0 {
		age = 0
	}
	return n, age, nil
}
func (s *PgStore) RetryCustomDomainVerification(ctx context.Context, domain string) error {
	// Retry accelerates the next probe but never extends ownership. Otherwise
	// an account could refresh a pending row forever and turn a seven-day
	// challenge into a permanent global domain reservation.
	tag, err := s.pool.Exec(ctx, `update custom_domains set verification_next_check_at=now(),verification_attempts=0 where domain=$1 and verified_at is null and verification_expires_at>now()`, domain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) CreateCustomDomainIfUnderQuota(ctx context.Context, domain, appID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	return m.createCustomDomainIfUnderQuota(ctx, domain, appID, "", token, appLimit, accountLimit)
}

func (m *MemStore) CreateCustomDomainInEnvironmentIfUnderQuota(ctx context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	if environmentID == "" {
		return CustomDomain{}, ErrInvalidArgument
	}
	return m.createCustomDomainIfUnderQuota(ctx, domain, appID, environmentID, token, appLimit, accountLimit)
}

func (m *MemStore) createCustomDomainIfUnderQuota(_ context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createCustomDomainIfUnderQuotaLocked(domain, appID, environmentID, token, appLimit, accountLimit)
}

func (m *MemStore) CreateCustomDomainIfUnderQuotaWithActivity(ctx context.Context, domain, appID, token string, appLimit, accountLimit int, entry OrgActivity) (CustomDomain, int64, error) {
	return m.createCustomDomainWithActivity(ctx, domain, appID, "", token, appLimit, accountLimit, entry)
}

func (m *MemStore) CreateCustomDomainInEnvironmentIfUnderQuotaWithActivity(ctx context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int, entry OrgActivity) (CustomDomain, int64, error) {
	if environmentID == "" {
		return CustomDomain{}, 0, ErrInvalidArgument
	}
	return m.createCustomDomainWithActivity(ctx, domain, appID, environmentID, token, appLimit, accountLimit, entry)
}

func (m *MemStore) createCustomDomainWithActivity(_ context.Context, domain, appID, environmentID, token string, appLimit, accountLimit int, entry OrgActivity) (CustomDomain, int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return CustomDomain{}, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.createCustomDomainIfUnderQuotaLocked(domain, appID, environmentID, token, appLimit, accountLimit)
	if err != nil {
		return CustomDomain{}, 0, err
	}
	return d, m.enqueueOrgActivityOutboxLocked(entry), nil
}

func (m *MemStore) createCustomDomainIfUnderQuotaLocked(domain, appID, environmentID, token string, appLimit, accountLimit int) (CustomDomain, error) {
	a, ok := m.apps[appID]
	if !ok {
		return CustomDomain{}, ErrNotFound
	}
	if environmentID != "" {
		environment, exists := m.projectEnvironments[environmentID]
		if !exists {
			environment, exists = m.projectEnvironments[strings.ReplaceAll(environmentID, "-", "")]
		}
		if !exists || environment.AccountID != a.AccountID || environment.ProjectID == "" || environment.ProjectID != a.ProjectID {
			return CustomDomain{}, ErrNotFound
		}
	}
	now := time.Now()
	if current, exists := m.domains[domain]; exists {
		if current.Verified() || current.VerificationExpiresAt.IsZero() || current.VerificationExpiresAt.After(now) {
			return CustomDomain{}, ErrConflict
		}
	}
	ac, ap := 0, 0
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
	d := CustomDomain{Domain: domain, AppID: appID, EnvironmentID: environmentID, ChallengeToken: token, CertStatus: CustomDomainCertPending, VerificationNextCheckAt: now, VerificationExpiresAt: now.Add(7 * 24 * time.Hour)}
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
	if !ok || d.Verified() || d.VerificationExpiresAt.IsZero() || !d.VerificationExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	d.VerificationAttempts = 0
	d.VerificationNextCheckAt = time.Now()
	m.domains[domain] = d
	return nil
}

func (m *MemStore) CustomDomainVerificationStats(_ context.Context) (int, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	var oldest time.Time
	for _, d := range m.domains {
		if d.Verified() || (!d.VerificationExpiresAt.IsZero() && !d.VerificationExpiresAt.After(now)) {
			continue
		}
		n++
		if oldest.IsZero() || d.VerificationNextCheckAt.Before(oldest) {
			oldest = d.VerificationNextCheckAt
		}
	}
	if oldest.IsZero() {
		return n, 0, nil
	}
	age := now.Sub(oldest)
	if age < 0 {
		age = 0
	}
	return n, age, nil
}

var _ CustomDomainChallengeVerifier = (*PgStore)(nil)
var _ CustomDomainChallengeVerifier = (*MemStore)(nil)
