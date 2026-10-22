package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FinancialObjectStorageEstimate is apid's live object-storage estimate for
// one account and UTC month (ADR-20261020160000): the cumulative usage, the
// pricing and plan allowance it was priced with, and whether the usage was
// fresh. UsageThrough is the oldest provider report the usage includes.
type FinancialObjectStorageEstimate struct {
	AccountID    string
	PeriodStart  time.Time
	ObservedAt   time.Time
	UsageThrough time.Time
	Fresh        bool
	// Usage carries StoredByteHours, RequestCount and EgressBytes only.
	Usage     api.ObjectStorageUsage
	Pricing   api.ObjectStoragePricing
	Allowance api.ObjectStorageAllowance
}

// FinancialObjectStorageEstimateStore keeps the newest estimate per account
// and month. The getter returns ErrNotFound when apid has recorded none.
type FinancialObjectStorageEstimateStore interface {
	PutFinancialObjectStorageEstimate(context.Context, FinancialObjectStorageEstimate) error
	FinancialObjectStorageEstimate(ctx context.Context, account string, period time.Time) (FinancialObjectStorageEstimate, error)
}

var (
	_ FinancialObjectStorageEstimateStore = (*PgStore)(nil)
	_ FinancialObjectStorageEstimateStore = (*MemStore)(nil)
)

func validFinancialObjectStorageEstimate(e FinancialObjectStorageEstimate) bool {
	start := e.PeriodStart.UTC()
	u := e.Usage
	return uuid.Validate(e.AccountID) == nil && !start.IsZero() && start.Equal(ObjectStoragePeriod(start)) &&
		!e.ObservedAt.Before(start) && !e.UsageThrough.Before(start) && !e.UsageThrough.After(e.ObservedAt) &&
		u.StoredByteHours >= 0 && u.RequestCount >= 0 && u.EgressBytes >= 0 &&
		e.Pricing.Valid() && e.Pricing.Currency == api.BillingCurrency && e.Allowance.Valid()
}

func (s *PgStore) PutFinancialObjectStorageEstimate(ctx context.Context, e FinancialObjectStorageEstimate) error {
	if !validFinancialObjectStorageEstimate(e) {
		return ErrInvalidArgument
	}
	aid, _ := parsePgUUID(e.AccountID)
	return sqlc.New().FinancialObjectStorageEstimatePut(ctx, s.pool, sqlc.FinancialObjectStorageEstimatePutParams{
		AccountID: aid, PeriodStart: financialTimestamp(e.PeriodStart), ObservedAt: financialTimestamp(e.ObservedAt),
		UsageThrough: financialTimestamp(e.UsageThrough), Fresh: e.Fresh, StoredByteHours: e.Usage.StoredByteHours,
		RequestCount: e.Usage.RequestCount, EgressBytes: e.Usage.EgressBytes, Currency: e.Pricing.Currency,
		StorageMillicentsPerGibMonth: e.Pricing.StorageMillicentsPerGiBMonth, RequestsMillicentsPerMillion: e.Pricing.RequestsMillicentsPerMillion,
		EgressMillicentsPerGib: e.Pricing.EgressMillicentsPerGiB, IncludedByteHours: e.Allowance.StoredByteHours,
		IncludedRequests: e.Allowance.Requests, IncludedEgressBytes: e.Allowance.EgressBytes,
	})
}

func (s *PgStore) FinancialObjectStorageEstimate(ctx context.Context, account string, period time.Time) (FinancialObjectStorageEstimate, error) {
	if uuid.Validate(account) != nil || period.IsZero() {
		return FinancialObjectStorageEstimate{}, ErrInvalidArgument
	}
	aid, _ := parsePgUUID(account)
	row, err := sqlc.New().FinancialObjectStorageEstimateGet(ctx, s.pool, sqlc.FinancialObjectStorageEstimateGetParams{AccountID: aid, PeriodStart: financialTimestamp(ObjectStoragePeriod(period))})
	if errors.Is(err, pgx.ErrNoRows) {
		return FinancialObjectStorageEstimate{}, ErrNotFound
	}
	if err != nil {
		return FinancialObjectStorageEstimate{}, err
	}
	return FinancialObjectStorageEstimate{
		AccountID: pgUUIDString(row.AccountID), PeriodStart: row.PeriodStart.Time.UTC(), ObservedAt: row.ObservedAt.Time.UTC(),
		UsageThrough: row.UsageThrough.Time.UTC(), Fresh: row.Fresh,
		Usage:     api.ObjectStorageUsage{StoredByteHours: row.StoredByteHours, RequestCount: row.RequestCount, EgressBytes: row.EgressBytes},
		Pricing:   api.ObjectStoragePricing{Currency: row.Currency, StorageMillicentsPerGiBMonth: row.StorageMillicentsPerGibMonth, RequestsMillicentsPerMillion: row.RequestsMillicentsPerMillion, EgressMillicentsPerGiB: row.EgressMillicentsPerGib},
		Allowance: api.ObjectStorageAllowance{StoredByteHours: row.IncludedByteHours, Requests: row.IncludedRequests, EgressBytes: row.IncludedEgressBytes},
	}, nil
}

func financialObjectStorageEstimateKey(account string, period time.Time) string {
	return account + "\n" + ObjectStoragePeriod(period).Format(time.RFC3339)
}

// PutFinancialObjectStorageEstimate mirrors PgStore: the newest observation wins.
func (m *MemStore) PutFinancialObjectStorageEstimate(_ context.Context, e FinancialObjectStorageEstimate) error {
	if !validFinancialObjectStorageEstimate(e) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := financialObjectStorageEstimateKey(e.AccountID, e.PeriodStart)
	if old, ok := m.financialObjectStorageEstimates[key]; ok && old.ObservedAt.After(e.ObservedAt) {
		return nil
	}
	if m.financialObjectStorageEstimates == nil {
		m.financialObjectStorageEstimates = map[string]FinancialObjectStorageEstimate{}
	}
	e.PeriodStart, e.ObservedAt, e.UsageThrough = e.PeriodStart.UTC(), e.ObservedAt.UTC(), e.UsageThrough.UTC()
	e.Usage = api.ObjectStorageUsage{StoredByteHours: e.Usage.StoredByteHours, RequestCount: e.Usage.RequestCount, EgressBytes: e.Usage.EgressBytes}
	m.financialObjectStorageEstimates[key] = e
	return nil
}

// FinancialObjectStorageEstimate mirrors PgStore.
func (m *MemStore) FinancialObjectStorageEstimate(_ context.Context, account string, period time.Time) (FinancialObjectStorageEstimate, error) {
	if uuid.Validate(account) != nil || period.IsZero() {
		return FinancialObjectStorageEstimate{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.financialObjectStorageEstimates[financialObjectStorageEstimateKey(account, period)]
	if !ok {
		return FinancialObjectStorageEstimate{}, ErrNotFound
	}
	return e, nil
}
