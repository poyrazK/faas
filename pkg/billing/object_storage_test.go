package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectStorageBillingAccounts struct {
	accounts []state.Account
}

func (s objectStorageBillingAccounts) ListAllAccounts(context.Context) ([]state.Account, error) {
	return s.accounts, nil
}

type objectStorageLineItemRecorder struct {
	records []state.ObjectStorageBillingRecord
	err     error
}

func (s *objectStorageLineItemRecorder) PublishObjectStorageLineItem(_ context.Context, record state.ObjectStorageBillingRecord) error {
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, record)
	return nil
}

type objectStorageBillingStore struct {
	snapshots map[string]state.ObjectUsageSnapshot
	records   map[string]state.ObjectStorageBillingRecord
}

func (s *objectStorageBillingStore) ObjectUsageForPeriod(_ context.Context, account string, _ time.Time) (state.ObjectUsageSnapshot, error) {
	return s.snapshots[account], nil
}

func (s *objectStorageBillingStore) GetObjectStorageBillingPeriod(_ context.Context, account string, period time.Time) (state.ObjectStorageBillingRecord, error) {
	record, ok := s.records[account+period.UTC().Format(time.RFC3339)]
	if !ok {
		return state.ObjectStorageBillingRecord{}, state.ErrNotFound
	}
	return record, nil
}

func (s *objectStorageBillingStore) RecordObjectStorageBillingPeriod(_ context.Context, record state.ObjectStorageBillingRecord) (state.ObjectStorageBillingRecord, error) {
	if s.records == nil {
		s.records = map[string]state.ObjectStorageBillingRecord{}
	}
	if record.ID == "" {
		record.ID = "record-1"
	}
	key := record.AccountID + record.PeriodStart.UTC().Format(time.RFC3339)
	if existing, ok := s.records[key]; ok {
		return existing, nil
	}
	s.records[key] = record
	return record, nil
}

func objectStorageBillingSnapshot(account, backend string, period time.Time) state.ObjectUsageSnapshot {
	return state.ObjectUsageSnapshot{
		Buckets: []state.ObjectBucketUsage{{Bucket: state.ObjectBucket{ID: "bucket-1", AccountID: account, BackendID: backend, BackendFingerprint: "fp", State: "ready", CreatedAt: period.Add(-time.Hour)}}},
		Reports: []api.ObjectStorageUsageReport{{AccountID: account, BackendID: backend, BackendFingerprint: "fp", Source: "provider", PeriodStart: period, ObservedAt: period.AddDate(0, 1, 0).Add(-time.Minute), StoredByteHours: 1}},
	}
}

func TestFinalizeObjectStoragePeriodIsClosedAndIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	first, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, period, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, period, now.Add(time.Hour))
	if err != nil || second.ID != first.ID || second.TotalMillicents != first.TotalMillicents {
		t.Fatalf("retry = %#v, err=%v", second, err)
	}
	if _, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, state.ObjectStoragePeriod(now), now); !errors.Is(err, state.ErrObjectBillingOpen) {
		t.Fatalf("open period error = %v", err)
	}
}

func TestFinalizeObjectStoragePeriodsPublishesOnlyActiveAccounts(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{
		"active": objectStorageBillingSnapshot("active", "backend", period),
		"empty":  {},
	}}
	sink := &objectStorageLineItemRecorder{}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active"}, {ID: "empty"}}}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].AccountID != "active" {
		t.Fatalf("finalized records = %#v, want one active account", records)
	}
	if len(sink.records) != 1 || sink.records[0].ID != records[0].ID {
		t.Fatalf("published records = %#v, want finalized record", sink.records)
	}
}

func TestFinalizeObjectStoragePeriodsPublishesExistingRecordOnRetry(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": objectStorageBillingSnapshot("active", "backend", period)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active"}}}
	sink := &objectStorageLineItemRecorder{}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now.Add(time.Hour), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 2 || sink.records[0].ID != sink.records[1].ID {
		t.Fatalf("retry published IDs = %#v, want same durable id", sink.records)
	}
}
