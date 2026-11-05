package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// ConsumerKeyQuota is a plan's unrevoked consumer key allowance: per app,
// and per billing subject of the app (ADR-202610171904).
type ConsumerKeyQuota struct {
	PerApp     int
	PerAccount int
}

// ConsumerKeyQuotaError is returned by CreateConsumerKeyForConsumerIfUnderQuota
// when the consumer's app, or its billing subject, holds its allowance of
// unrevoked keys already. Scope is api.PlanQuotaScopeApp or
// api.PlanQuotaScopeAccount.
type ConsumerKeyQuotaError struct {
	Scope    string
	Limit    int
	Observed int
}

func (e *ConsumerKeyQuotaError) Error() string {
	return fmt.Sprintf("state: consumer key quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

func (q ConsumerKeyQuota) check(appKeys, subjectKeys int) error {
	if appKeys >= q.PerApp {
		return &ConsumerKeyQuotaError{Scope: api.PlanQuotaScopeApp, Limit: q.PerApp, Observed: appKeys}
	}
	if subjectKeys >= q.PerAccount {
		return &ConsumerKeyQuotaError{Scope: api.PlanQuotaScopeAccount, Limit: q.PerAccount, Observed: subjectKeys}
	}
	return nil
}

func (s *PgStore) CreateConsumerKeyForConsumerIfUnderQuota(ctx context.Context, accountID, consumerID, name, prefix string, hash []byte, scopes []string,
	expiresAt *time.Time, quota ConsumerKeyQuota) (ConsumerKey, error) {
	if err := validatePgConsumerKeyForConsumer(accountID, consumerID, name, prefix, hash, scopes, expiresAt); err != nil {
		return ConsumerKey{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConsumerKey{}, fmt.Errorf("state: begin consumer key issue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var appID string
	err = tx.QueryRow(ctx, `select app_id::text from api_consumers where id = $1::uuid and account_id = $2::uuid`, consumerID, accountID).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumerKey{}, ErrNotFound
	}
	if err != nil {
		return ConsumerKey{}, fmt.Errorf("state: read consumer %s app: %w", consumerID, err)
	}
	// The billing subject's lock serializes every issue that spends its
	// allowance, so the counts below hold until the insert commits.
	subject, _, err := lockQuotaSubjectTx(ctx, tx, appID)
	if err != nil {
		return ConsumerKey{}, err
	}
	var appKeys, subjectKeys int
	err = tx.QueryRow(ctx, `select count(*) filter (where k.app_id = $2::uuid), count(*)
		from consumer_keys k
		join apps a on a.id = k.app_id
		left join orgs o on o.id = a.org_id
		where k.revoked_at is null and (a.account_id = $1::uuid or a.org_id = $1::uuid)
		  and `+quotaSubjectSQL+` = $1::uuid and a.status <> 'deleted'`, subject, appID).Scan(&appKeys, &subjectKeys)
	if err != nil {
		return ConsumerKey{}, fmt.Errorf("state: count consumer keys of billing subject %s: %w", subject, err)
	}
	if err := quota.check(appKeys, subjectKeys); err != nil {
		return ConsumerKey{}, err
	}
	k, err := insertConsumerKeyForConsumer(ctx, tx, accountID, consumerID, name, prefix, hash, scopes, expiresAt)
	if err != nil {
		return ConsumerKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConsumerKey{}, fmt.Errorf("state: commit consumer key: %w", err)
	}
	return k, nil
}

func (m *MemStore) CreateConsumerKeyForConsumerIfUnderQuota(_ context.Context, accountID, consumerID, name, prefix string, hash []byte, scopes []string,
	expiresAt *time.Time, quota ConsumerKeyQuota) (ConsumerKey, error) {
	if err := validateConsumerKeyForConsumer("CreateConsumerKeyForConsumerIfUnderQuota", accountID, consumerID, name, prefix, hash, scopes, expiresAt); err != nil {
		return ConsumerKey{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return ConsumerKey{}, ErrNotFound
	}
	app, ok := m.apps[c.AppID]
	if !ok || app.Status == AppDeleted {
		return ConsumerKey{}, ErrNotFound
	}
	subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
	appKeys, subjectKeys := 0, 0
	for _, k := range m.consumerKeys {
		if k.RevokedAt != nil || !m.countsTowardQuotaSubjectLocked(k.AccountID, k.AppID, subject) {
			continue
		}
		subjectKeys++
		if k.AppID == app.ID {
			appKeys++
		}
	}
	if err := quota.check(appKeys, subjectKeys); err != nil {
		return ConsumerKey{}, err
	}
	return m.createConsumerKeyForConsumerLocked(accountID, consumerID, name, prefix, hash, scopes, expiresAt)
}
