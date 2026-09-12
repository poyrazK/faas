// adr: 156 — object-storage month close and provider delivery rollout.
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
	policy  ObjectStorageLineItemPolicy
}

func (s *objectStorageLineItemRecorder) ObjectStorageLineItemPolicy() (ObjectStorageLineItemPolicy, bool) {
	if s.policy.Provider == "" {
		return ObjectStorageLineItemPolicy{
			Provider:      "test",
			Mode:          MeterDeliveryLive,
			EffectiveFrom: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		}, true
	}
	return s.policy, true
}

func (s *objectStorageLineItemRecorder) PublishObjectStorageLineItem(_ context.Context, record state.ObjectStorageBillingRecord) error {
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, record)
	return nil
}

type objectStorageBillingStore struct {
	snapshots  map[string]state.ObjectUsageSnapshot
	records    map[string]state.ObjectStorageBillingRecord
	deliveries map[string]state.ObjectStorageBillingDelivery
}

func (s *objectStorageBillingStore) GetObjectStorageBillingDelivery(_ context.Context, provider, recordID string) (state.ObjectStorageBillingDelivery, error) {
	delivery, ok := s.deliveries[provider+recordID]
	if !ok {
		return state.ObjectStorageBillingDelivery{}, state.ErrNotFound
	}
	return delivery, nil
}

func (s *objectStorageBillingStore) RecordObjectStorageBillingDelivery(_ context.Context, delivery state.ObjectStorageBillingDelivery) (state.ObjectStorageBillingDelivery, error) {
	if s.deliveries == nil {
		s.deliveries = map[string]state.ObjectStorageBillingDelivery{}
	}
	key := delivery.Provider + delivery.BillingRecordID
	if existing, ok := s.deliveries[key]; ok {
		return existing, nil
	}
	s.deliveries[key] = delivery
	return delivery, nil
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
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}, {ID: "empty", Plan: api.PlanHobby}}}
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
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now.Add(time.Hour), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 1 {
		t.Fatalf("retry published %d records, want durable receipt to suppress duplicate", len(sink.records))
	}
}

func TestFinalizeObjectStoragePeriodsShadowsWithoutPublishing(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": objectStorageBillingSnapshot("active", "backend", period)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{policy: ObjectStorageLineItemPolicy{Provider: "polar", Mode: MeterDeliveryShadow, EffectiveFrom: period}}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(sink.records) != 0 {
		t.Fatalf("records=%d published=%d, want one local record and no event", len(records), len(sink.records))
	}
	delivery := store.deliveries["polar"+records[0].ID]
	if delivery.Mode != state.ObjectStorageDeliveryShadow || delivery.QuantityMillicents != records[0].TotalMillicents {
		t.Fatalf("shadow delivery = %+v", delivery)
	}
}

func TestFinalizeObjectStoragePeriodsNeverPublishesBeforeActivation(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": objectStorageBillingSnapshot("active", "backend", period)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{policy: ObjectStorageLineItemPolicy{Provider: "polar", Mode: MeterDeliveryLive, EffectiveFrom: period.AddDate(0, 1, 0)}}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink)
	if err != nil {
		t.Fatal(err)
	}
	delivery := store.deliveries["polar"+records[0].ID]
	if len(sink.records) != 0 || delivery.Mode != state.ObjectStorageDeliveryPreActivation || delivery.QuantityMillicents != 0 {
		t.Fatalf("published=%d preactivation delivery=%+v", len(sink.records), delivery)
	}
}

func TestFinalizeObjectStoragePeriodsNeverBillsIneligiblePlan(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"free": objectStorageBillingSnapshot("free", "backend", period)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "free", Plan: api.PlanFree}}}
	sink := &objectStorageLineItemRecorder{policy: ObjectStorageLineItemPolicy{Provider: "polar", Mode: MeterDeliveryLive, EffectiveFrom: period}}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink)
	if err != nil {
		t.Fatal(err)
	}
	delivery := store.deliveries["polar"+records[0].ID]
	if len(sink.records) != 0 || delivery.Mode != state.ObjectStorageDeliveryPlanIneligible || delivery.QuantityMillicents != 0 {
		t.Fatalf("published=%d ineligible delivery=%+v", len(sink.records), delivery)
	}
}

func TestFinalizeObjectStoragePeriodsRetriesFailedLiveDelivery(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": objectStorageBillingSnapshot("active", "backend", period)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{err: errors.New("provider unavailable")}
	pricing := api.ObjectStoragePricing{Currency: "EUR", StorageMillicentsPerGiBMonth: 1000}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now, sink); err == nil {
		t.Fatal("failed live delivery returned nil error")
	}
	if len(store.deliveries) != 0 {
		t.Fatalf("failed live delivery recorded receipt: %+v", store.deliveries)
	}
	sink.err = nil
	if records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, now.Add(time.Minute), sink); err != nil || len(records) != 1 || len(sink.records) != 1 {
		t.Fatalf("retry records=%d published=%d err=%v", len(records), len(sink.records), err)
	}
}
