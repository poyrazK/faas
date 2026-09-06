package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const objectStorageBillingSelect = `
select id, account_id, period_start, period_end, currency,
       stored_byte_hours, request_count, egress_bytes,
       provider_cost_millicents,
       storage_millicents_per_gib_month, requests_millicents_per_million,
       egress_millicents_per_gib,
       storage_millicents, requests_millicents, egress_millicents,
       total_millicents, finalized_at
  from object_storage_billing_periods
 where account_id = $1 and period_start = $2`

func (s *PgStore) GetObjectStorageBillingPeriod(ctx context.Context, account string, periodStart time.Time) (ObjectStorageBillingRecord, error) {
	var record ObjectStorageBillingRecord
	err := s.pool.QueryRow(ctx, objectStorageBillingSelect, account, ObjectStoragePeriod(periodStart)).Scan(
		&record.ID, &record.AccountID, &record.PeriodStart, &record.PeriodEnd, &record.Currency,
		&record.StoredByteHours, &record.RequestCount, &record.EgressBytes,
		&record.ProviderCostMillicents,
		&record.StorageMillicentsPerGiBMonth, &record.RequestsMillicentsPerMillion,
		&record.EgressMillicentsPerGiB,
		&record.StorageMillicents, &record.RequestsMillicents, &record.EgressMillicents,
		&record.TotalMillicents, &record.FinalizedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectStorageBillingRecord{}, ErrNotFound
	}
	if err != nil {
		return ObjectStorageBillingRecord{}, err
	}
	return record, nil
}

func (s *PgStore) RecordObjectStorageBillingPeriod(ctx context.Context, record ObjectStorageBillingRecord) (ObjectStorageBillingRecord, error) {
	record = normalizeObjectStorageBillingRecord(record)
	if err := validateObjectStorageBillingRecord(record); err != nil {
		return ObjectStorageBillingRecord{}, err
	}
	const insert = `
insert into object_storage_billing_periods (
    account_id, period_start, period_end, currency,
    stored_byte_hours, request_count, egress_bytes, provider_cost_millicents,
    storage_millicents_per_gib_month, requests_millicents_per_million,
    egress_millicents_per_gib, storage_millicents, requests_millicents,
    egress_millicents, total_millicents, finalized_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
on conflict (account_id, period_start) do nothing
returning id, account_id, period_start, period_end, currency,
          stored_byte_hours, request_count, egress_bytes,
          provider_cost_millicents,
          storage_millicents_per_gib_month, requests_millicents_per_million,
          egress_millicents_per_gib,
          storage_millicents, requests_millicents, egress_millicents,
          total_millicents, finalized_at`
	var stored ObjectStorageBillingRecord
	err := s.pool.QueryRow(ctx, insert,
		record.AccountID, record.PeriodStart, record.PeriodEnd, record.Currency,
		record.StoredByteHours, record.RequestCount, record.EgressBytes, record.ProviderCostMillicents,
		record.StorageMillicentsPerGiBMonth, record.RequestsMillicentsPerMillion, record.EgressMillicentsPerGiB,
		record.StorageMillicents, record.RequestsMillicents, record.EgressMillicents,
		record.TotalMillicents, record.FinalizedAt,
	).Scan(
		&stored.ID, &stored.AccountID, &stored.PeriodStart, &stored.PeriodEnd, &stored.Currency,
		&stored.StoredByteHours, &stored.RequestCount, &stored.EgressBytes,
		&stored.ProviderCostMillicents,
		&stored.StorageMillicentsPerGiBMonth, &stored.RequestsMillicentsPerMillion,
		&stored.EgressMillicentsPerGiB,
		&stored.StorageMillicents, &stored.RequestsMillicents, &stored.EgressMillicents,
		&stored.TotalMillicents, &stored.FinalizedAt,
	)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectStorageBillingRecord{}, err
	}
	// A concurrent retry may have won the unique key. Returning the existing
	// row is safe only when the financial snapshot is identical.
	existing, err := s.GetObjectStorageBillingPeriod(ctx, record.AccountID, record.PeriodStart)
	if err != nil {
		return ObjectStorageBillingRecord{}, err
	}
	if !sameObjectStorageBillingRecord(existing, record) {
		return ObjectStorageBillingRecord{}, ErrObjectBillingConflict
	}
	return existing, nil
}
