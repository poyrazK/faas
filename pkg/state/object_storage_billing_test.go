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
		{BackendID: "external-a", BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PeriodStart: period, ObservedAt: period.AddDate(0, 0, 30), StoredByteHours: 10},
	}}
	if _, err := state.SummarizeObjectStorageBillingUsage(snapshot, period); !errors.Is(err, state.ErrObjectBillingIncomplete) {
		t.Fatalf("missing backend report error = %v", err)
	}
	snapshot.Reports = append(snapshot.Reports, api.ObjectStorageUsageReport{
		BackendID: "external-b", BackendFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PeriodStart: period, ObservedAt: period.AddDate(0, 0, 30), RequestCount: 7,
	})
	usage, err := state.SummarizeObjectStorageBillingUsage(snapshot, period)
	if err != nil || usage.StoredByteHours != 10 || usage.RequestCount != 7 || !usage.Fresh {
		t.Fatalf("complete usage = %#v, err=%v", usage, err)
	}
}

func TestMemObjectStorageBillingPeriodIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	record := state.ObjectStorageBillingRecord{
		AccountID: "acct", PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Currency: "EUR", FinalizedAt: period.AddDate(0, 1, 0).Add(time.Hour),
	}
	if got, err := store.RecordObjectStorageBillingPeriod(nil, record); err != nil || got.ID == "" {
		t.Fatalf("first record = %#v, err=%v", got, err)
	} else if again, err := store.RecordObjectStorageBillingPeriod(nil, record); err != nil || again.ID != got.ID {
		t.Fatalf("idempotent retry = %#v, err=%v", again, err)
	}
	conflict := record
	conflict.Currency = "USD"
	if _, err := store.RecordObjectStorageBillingPeriod(nil, conflict); !errors.Is(err, state.ErrObjectBillingConflict) {
		t.Fatalf("changed snapshot error = %v", err)
	}
}
