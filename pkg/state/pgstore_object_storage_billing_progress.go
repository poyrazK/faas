package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) GetObjectStorageBillingProgress(ctx context.Context, provider, accountID string, periodStart time.Time, mode string) (ObjectStorageBillingProgress, error) {
	p := ObjectStorageBillingProgress{Provider: provider, AccountID: accountID, PeriodStart: ObjectStoragePeriod(periodStart), Mode: mode}
	err := s.pool.QueryRow(ctx, `
		select delivered_millicents, pending_millicents, pending_fixed, credited_millicents, version, updated_at
		  from object_storage_billing_progress
		 where provider = $1 and account_id = $2 and period_start = $3 and mode = $4`,
		provider, accountID, p.PeriodStart, mode).
		Scan(&p.DeliveredMillicents, &p.PendingMillicents, &p.PendingFixed, &p.CreditedMillicents, &p.Version, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectStorageBillingProgress{}, ErrNotFound
	}
	if err != nil {
		return ObjectStorageBillingProgress{}, fmt.Errorf("state: read object storage billing progress: %w", mapErr(err))
	}
	return p, nil
}

func (s *PgStore) SaveObjectStorageBillingProgress(ctx context.Context, p ObjectStorageBillingProgress) (ObjectStorageBillingProgress, error) {
	p, err := validateObjectStorageBillingProgress(p)
	if err != nil {
		return ObjectStorageBillingProgress{}, err
	}
	var row pgx.Row
	if p.Version == 0 {
		row = s.pool.QueryRow(ctx, `
			insert into object_storage_billing_progress
			       (provider, account_id, period_start, mode, delivered_millicents, pending_millicents, pending_fixed, version)
			values ($1, $2, $3, $4, $5, $6, $7, 1)
			on conflict (provider, account_id, period_start, mode) do nothing
			returning credited_millicents, version, updated_at`,
			p.Provider, p.AccountID, p.PeriodStart, p.Mode, p.DeliveredMillicents, p.PendingMillicents, p.PendingFixed)
	} else {
		// Delivered millicents were billed; a write may never lower them.
		row = s.pool.QueryRow(ctx, `
			update object_storage_billing_progress
			   set delivered_millicents = $5, pending_millicents = $6, pending_fixed = $7,
			       version = version + 1, updated_at = now()
			 where provider = $1 and account_id = $2 and period_start = $3 and mode = $4
			   and version = $8 and delivered_millicents <= $5
			returning credited_millicents, version, updated_at`,
			p.Provider, p.AccountID, p.PeriodStart, p.Mode, p.DeliveredMillicents, p.PendingMillicents, p.PendingFixed, p.Version)
	}
	if err := row.Scan(&p.CreditedMillicents, &p.Version, &p.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return ObjectStorageBillingProgress{}, ErrConflict
	} else if err != nil {
		return ObjectStorageBillingProgress{}, fmt.Errorf("state: save object storage billing progress: %w", mapErr(err))
	}
	return p, nil
}

func (s *PgStore) LiveObjectStorageMillicents(ctx context.Context, accountID string, periodStart time.Time) (int64, int64, error) {
	var delivered, pending int64
	err := s.pool.QueryRow(ctx, `
		select coalesce(sum(delivered_millicents), 0)::bigint, coalesce(sum(pending_millicents), 0)::bigint
		  from object_storage_billing_progress
		 where account_id = $1 and period_start = $2 and mode = 'live'`,
		accountID, ObjectStoragePeriod(periodStart)).Scan(&delivered, &pending)
	if err != nil {
		return 0, 0, fmt.Errorf("state: read live object storage charge: %w", mapErr(err))
	}
	return delivered, pending, nil
}
