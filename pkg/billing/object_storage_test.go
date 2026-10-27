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
	charges []int64
	// steps is every publish attempt, failed ones included.
	steps  []ObjectStorageChargeStep
	err    error
	policy ObjectStorageLineItemPolicy
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

func (s *objectStorageLineItemRecorder) PublishObjectStorageCharge(_ context.Context, step ObjectStorageChargeStep) error {
	s.steps = append(s.steps, step)
	if s.err != nil {
		return s.err
	}
	if step.Record != nil {
		s.records = append(s.records, *step.Record)
	}
	s.charges = append(s.charges, step.TargetMillicents-step.DeliveredMillicents)
	return nil
}

type objectStorageBillingStore struct {
	snapshots  map[string]state.ObjectUsageSnapshot
	records    map[string]state.ObjectStorageBillingRecord
	deliveries map[string]state.ObjectStorageBillingDelivery
	// caps and usage back the overage cap reads (ADR-1320); usage rows
	// carry their hour in Month.
	caps  map[string]int64
	usage map[string][]state.Usage
	// progress is the charge ledger (ADR-20261027093220), written at now.
	progress map[string]state.ObjectStorageBillingProgress
	now      time.Time
	// beforeSave, when set, runs before each progress write.
	beforeSave func(state.ObjectStorageBillingProgress)
	// reservedAtCapRead is the account's live reservation each usage read saw.
	reservedAtCapRead []int64
}

func objectStorageProgressKey(provider, account string, period time.Time, mode string) string {
	return provider + "/" + account + "/" + period.UTC().Format(time.RFC3339) + "/" + mode
}

func (s *objectStorageBillingStore) GetObjectStorageBillingProgress(_ context.Context, provider, account string, period time.Time, mode string) (state.ObjectStorageBillingProgress, error) {
	p, ok := s.progress[objectStorageProgressKey(provider, account, period, mode)]
	if !ok {
		return state.ObjectStorageBillingProgress{}, state.ErrNotFound
	}
	return p, nil
}

func (s *objectStorageBillingStore) SaveObjectStorageBillingProgress(_ context.Context, p state.ObjectStorageBillingProgress) (state.ObjectStorageBillingProgress, error) {
	if s.beforeSave != nil {
		s.beforeSave(p)
	}
	if s.progress == nil {
		s.progress = map[string]state.ObjectStorageBillingProgress{}
	}
	key := objectStorageProgressKey(p.Provider, p.AccountID, p.PeriodStart, p.Mode)
	stored, exists := s.progress[key]
	if stored.Version != p.Version || exists != (p.Version != 0) || p.DeliveredMillicents < stored.DeliveredMillicents {
		return state.ObjectStorageBillingProgress{}, state.ErrConflict
	}
	p.CreditedMillicents = stored.CreditedMillicents
	p.Version++
	p.UpdatedAt = s.now
	s.progress[key] = p
	return p, nil
}

func (s *objectStorageBillingStore) LiveObjectStorageMillicents(_ context.Context, account string, period time.Time) (int64, int64, error) {
	var delivered, pending int64
	for _, p := range s.progress {
		if p.AccountID == account && p.Mode == state.ObjectStorageDeliveryLive && p.PeriodStart.Equal(period) {
			delivered += p.DeliveredMillicents
			pending += p.PendingMillicents
		}
	}
	return delivered, pending, nil
}

func (s *objectStorageBillingStore) liveReserved(account string, period time.Time) int64 {
	delivered, pending, _ := s.LiveObjectStorageMillicents(context.Background(), account, period)
	return delivered + pending
}

func (s *objectStorageBillingStore) GetAccountOverageCapCents(_ context.Context, account string) (int64, bool, error) {
	capCents, capped := s.caps[account]
	return capCents, capped, nil
}

func (s *objectStorageBillingStore) UsageByHour(_ context.Context, account string, start, end time.Time) ([]state.Usage, error) {
	s.reservedAtCapRead = append(s.reservedAtCapRead, s.liveReserved(account, state.ObjectStoragePeriod(start)))
	var out []state.Usage
	for _, row := range s.usage[account] {
		if !row.Month.Before(start) && row.Month.Before(end) {
			out = append(out, row)
		}
	}
	return out, nil
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

// billableObjectStorageSnapshot is a month of Hobby's included storage
// (ADR-1430) plus gibMonths GiB-months, which a 1000-millicent rate card
// prices at 1000 millicents each.
func billableObjectStorageSnapshot(account string, period time.Time, gibMonths int64) state.ObjectUsageSnapshot {
	snapshot := objectStorageBillingSnapshot(account, "backend", period)
	snapshot.Reports[0].StoredByteHours = api.MustLimitsFor(api.PlanHobby).ObjectStorage.Allowance().StoredByteHours + gibMonths*int64(1<<30)*api.ObjectStorageBillingHoursPerMonth
	return snapshot
}

func TestFinalizeObjectStoragePeriodIsClosedAndIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	first, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, api.ObjectStorageAllowance{}, period, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, api.ObjectStorageAllowance{}, period, now.Add(time.Hour))
	if err != nil || second.ID != first.ID || second.TotalMillicents != first.TotalMillicents {
		t.Fatalf("retry = %#v, err=%v", second, err)
	}
	if _, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, api.ObjectStorageAllowance{}, state.ObjectStoragePeriod(now), now); !errors.Is(err, state.ErrObjectBillingOpen) {
		t.Fatalf("open period error = %v", err)
	}
}

// adr: 1320 — month close prices usage above the plan allowance and copies the
// allowance into the immutable record.
func TestFinalizeObjectStoragePeriodRecordsAllowance(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	gibMonth := int64(1<<30) * api.ObjectStorageBillingHoursPerMonth
	snapshot := objectStorageBillingSnapshot("acct", "backend", period)
	snapshot.Reports[0].StoredByteHours = 3 * gibMonth
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"acct": snapshot}}
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
	allowance := api.ObjectStorageAllowance{StoredByteHours: gibMonth}
	record, err := FinalizeObjectStoragePeriod(context.Background(), store, "acct", pricing, allowance, period, period.AddDate(0, 1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if record.IncludedStoredByteHours != gibMonth || record.StoredByteHours != 3*gibMonth || record.StorageMillicents != 2000 || record.TotalMillicents != 2000 {
		t.Fatalf("record = %+v, want one included GiB-month and two billed", record)
	}
}

func TestLargestObjectStorageAllowanceTakesPerMeterMaximum(t *testing.T) {
	allowances := map[api.Plan]api.ObjectStorageAllowance{
		api.PlanPro:   {StoredByteHours: 100, Requests: 10, EgressBytes: 1},
		api.PlanHobby: {StoredByteHours: 50, Requests: 20, EgressBytes: 0},
	}
	lookup := func(plan api.Plan) (api.ObjectStorageAllowance, bool) {
		a, ok := allowances[plan]
		return a, ok
	}
	got, err := largestObjectStorageAllowance([]api.Plan{api.PlanPro, api.PlanHobby}, lookup)
	if err != nil || got != (api.ObjectStorageAllowance{StoredByteHours: 100, Requests: 20, EgressBytes: 1}) {
		t.Fatalf("allowance = %+v, %v; want the per-meter maximum so a downgrade never shrinks it", got, err)
	}
	if _, err := largestObjectStorageAllowance([]api.Plan{"unknown"}, lookup); err == nil {
		t.Fatal("unknown plan did not fail closed")
	}
}

func TestFinalizeObjectStoragePeriodsPublishesOnlyActiveAccounts(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{
		"active": billableObjectStorageSnapshot("active", period, 3),
		"empty":  {},
	}}
	sink := &objectStorageLineItemRecorder{}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}, {ID: "empty", Plan: api.PlanHobby}}}
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": billableObjectStorageSnapshot("active", period, 3)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{}
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"active": billableObjectStorageSnapshot("active", period, 3)}}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "active", Plan: api.PlanHobby}}}
	sink := &objectStorageLineItemRecorder{err: errors.New("provider unavailable")}
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
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
