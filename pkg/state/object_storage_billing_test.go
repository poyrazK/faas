// ADR-156: immutable object-storage billing and delivery receipts.
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSummarizeObjectStorageBillingUsageRequiresEveryBackend(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	snapshot := state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{
		{Bucket: state.ObjectBucket{BackendID: "external-a", BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: period.AddDate(0, 0, -1)}},
		{Bucket: state.ObjectBucket{BackendID: "external-b", BackendFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CreatedAt: period.AddDate(0, 0, -1)}},
	}, Reports: []api.ObjectStorageUsageReport{
		{BackendID: "external-a", BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PeriodStart: period, ObservedAt: period.AddDate(0, 1, 0).Add(-time.Microsecond), StoredByteHours: 10},
	}}
	if _, err := state.SummarizeObjectStorageBillingUsage(snapshot, period); !errors.Is(err, state.ErrObjectBillingIncomplete) {
		t.Fatalf("missing backend report error = %v", err)
	}
	snapshot.Reports = append(snapshot.Reports, api.ObjectStorageUsageReport{
		BackendID: "external-b", BackendFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PeriodStart: period, ObservedAt: period.AddDate(0, 1, 0).Add(-time.Microsecond), RequestCount: 7,
	})
	usage, err := state.SummarizeObjectStorageBillingUsage(snapshot, period)
	if err != nil || usage.StoredByteHours != 10 || usage.RequestCount != 7 || !usage.Fresh {
		t.Fatalf("complete usage = %#v, err=%v", usage, err)
	}
}

// Hunt 19 H19-2: a backend with a bucket live at the month's end must report
// to that end. Its latest report a day short used to be billed as the month,
// so a stalled exporter locked in an undercount. A backend whose buckets were
// all deleted keeps its last report.
func TestSummarizeObjectStorageBillingUsageRequiresCoverageToTheMonthEnd(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := period.AddDate(0, 1, 0)
	const fp = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	live := state.ObjectBucket{BackendID: "external-a", BackendFingerprint: fp, CreatedAt: period.AddDate(0, 0, -1)}
	deleted := live
	deleted.State, deleted.UpdatedAt = "deleted", period.AddDate(0, 0, 10)
	for _, tc := range []struct {
		name     string
		bucket   state.ObjectBucket
		observed time.Time
		wantErr  error
	}{
		{name: "live bucket, closing report", bucket: live, observed: end.Add(-time.Microsecond)},
		{name: "live bucket, report within tolerance", bucket: live, observed: end.Add(-api.ObjectStorageReportCloseTolerance)},
		{name: "live bucket, stalled exporter", bucket: live, observed: end.AddDate(0, 0, -1), wantErr: state.ErrObjectBillingIncomplete},
		{name: "bucket deleted mid-month", bucket: deleted, observed: period.AddDate(0, 0, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{{Bucket: tc.bucket}}, Reports: []api.ObjectStorageUsageReport{
				{BackendID: "external-a", BackendFingerprint: fp, PeriodStart: period, ObservedAt: tc.observed, StoredByteHours: 10},
			}}
			if _, err := state.SummarizeObjectStorageBillingUsage(snapshot, period); !errors.Is(err, tc.wantErr) {
				t.Fatalf("SummarizeObjectStorageBillingUsage error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// ADR-20261027093220: daily steps price what has been metered or reported so
// far, without the month-close coverage requirements.
func TestSummarizeObjectStorageUsageToDateNeedsNoCoverage(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	const fp = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	live := state.ObjectBucket{BackendID: "external-a", BackendFingerprint: fp, CreatedAt: period.AddDate(0, 0, -1)}
	other := state.ObjectBucket{BackendID: "external-b", BackendFingerprint: fp, CreatedAt: period.AddDate(0, 0, -1)}
	reported := state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{{Bucket: live}, {Bucket: other}}, Reports: []api.ObjectStorageUsageReport{
		{BackendID: "external-a", BackendFingerprint: fp, PeriodStart: period, ObservedAt: period.AddDate(0, 0, 3), StoredByteHours: 10},
		{BackendID: "external-a", BackendFingerprint: fp, PeriodStart: period, ObservedAt: period.AddDate(0, 0, 9), StoredByteHours: 25},
	}}
	if _, err := state.SummarizeObjectStorageBillingUsage(reported, period); !errors.Is(err, state.ErrObjectBillingIncomplete) {
		t.Fatalf("month close accepted a partial month: %v", err)
	}
	if usage, err := state.SummarizeObjectStorageUsageToDate(reported, period); err != nil || usage.StoredByteHours != 25 {
		t.Fatalf("reported usage to date = %+v, err = %v; want the latest report's 25", usage, err)
	}
	metered := state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{
		{Bucket: live, StoredByteHours: 40, StorageMeteredThrough: period.AddDate(0, 0, 9)},
	}}
	if _, err := state.SummarizeObjectStorageBillingUsage(metered, period); !errors.Is(err, state.ErrObjectBillingIncomplete) {
		t.Fatalf("month close accepted a partly metered month: %v", err)
	}
	if usage, err := state.SummarizeObjectStorageUsageToDate(metered, period); err != nil || usage.StoredByteHours != 40 {
		t.Fatalf("metered usage to date = %+v, err = %v; want 40", usage, err)
	}
}

func TestMemObjectStorageBillingPeriodIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	record := state.ObjectStorageBillingRecord{
		AccountID: "acct", PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Currency: "USD", FinalizedAt: period.AddDate(0, 1, 0).Add(time.Hour),
	}
	if got, err := store.RecordObjectStorageBillingPeriod(nil, record); err != nil || got.ID == "" {
		t.Fatalf("first record = %#v, err=%v", got, err)
	} else if again, err := store.RecordObjectStorageBillingPeriod(nil, record); err != nil || again.ID != got.ID {
		t.Fatalf("idempotent retry = %#v, err=%v", again, err)
	}
	conflict := record
	conflict.Currency = "EUR"
	if _, err := store.RecordObjectStorageBillingPeriod(nil, conflict); !errors.Is(err, state.ErrObjectBillingConflict) {
		t.Fatalf("changed snapshot error = %v", err)
	}
}

func TestMemObjectStorageBillingDeliveryIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	record, err := store.RecordObjectStorageBillingPeriod(nil, state.ObjectStorageBillingRecord{
		AccountID: "acct", PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Currency: "USD", FinalizedAt: period.AddDate(0, 1, 0).Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery := state.ObjectStorageBillingDelivery{
		Provider: "polar", BillingRecordID: record.ID, AccountID: record.AccountID,
		PeriodStart: period, Mode: state.ObjectStorageDeliveryShadow,
		QuantityMillicents: record.TotalMillicents, DeliveredAt: period.AddDate(0, 1, 0).Add(2 * time.Hour),
	}
	first, err := store.RecordObjectStorageBillingDelivery(nil, delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery.DeliveredAt = delivery.DeliveredAt.Add(time.Hour)
	again, err := store.RecordObjectStorageBillingDelivery(nil, delivery)
	if err != nil || !again.DeliveredAt.Equal(first.DeliveredAt) {
		t.Fatalf("idempotent retry = %+v, err=%v", again, err)
	}
	conflict := delivery
	conflict.Mode = state.ObjectStorageDeliveryLive
	if _, err := store.RecordObjectStorageBillingDelivery(nil, conflict); !errors.Is(err, state.ErrObjectBillingConflict) {
		t.Fatalf("changed delivery error = %v", err)
	}
}
