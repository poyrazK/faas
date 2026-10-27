package object

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020160000 — apid records each billing account's live
// object-storage estimate with the usage endpoint's pricing and current-plan
// allowance, zero usage included, and records nothing without pricing.
func TestRecordObjectStorageEstimates(t *testing.T) {
	st, registry, bucket, _ := receiptRecoveryFixture(t, "http://127.0.0.1:9")
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	ctx := t.Context()
	now := time.Now().UTC()
	idle, err := st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordObjectStorageEstimates(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FinancialObjectStorageEstimate(ctx, bucket.AccountID, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("estimate recorded without pricing: %v", err)
	}

	registry.Pricing = &api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 2300, RequestsMillicentsPerMillion: 400, EgressMillicentsPerGiB: 9000}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	// Reports are monotonic per backend: this one follows the fixture's.
	reportedAt := time.Now().UTC()
	if err := st.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: bucket.AccountID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(reportedAt), ObservedAt: reportedAt, StoredByteHours: 4096, RequestCount: 12, EgressBytes: 77}); err != nil {
		t.Fatal(err)
	}
	now = reportedAt.Add(time.Second)
	if err := svc.RecordObjectStorageEstimates(ctx, now); err != nil {
		t.Fatal(err)
	}
	e, err := st.FinancialObjectStorageEstimate(ctx, bucket.AccountID, now)
	if err != nil {
		t.Fatal(err)
	}
	if e.Usage.StoredByteHours != 4096 || e.Usage.RequestCount != 12 || e.Usage.EgressBytes != 77 || e.Pricing != *registry.Pricing ||
		e.Allowance != api.MustLimitsFor(api.PlanPro).ObjectStorage.Allowance() || !e.ObservedAt.Equal(now) || e.UsageThrough.After(now) || e.UsageThrough.Before(state.ObjectStoragePeriod(now)) {
		t.Fatalf("estimate = %+v", e)
	}
	zero, err := st.FinancialObjectStorageEstimate(ctx, idle.ID, now)
	if err != nil || zero.Usage.StoredByteHours != 0 || zero.Usage.RequestCount != 0 || !zero.UsageThrough.Equal(state.ObjectStoragePeriod(now)) {
		t.Fatalf("account without buckets: %+v, %v", zero, err)
	}

	// An older pass never replaces a newer estimate.
	if err := svc.RecordObjectStorageEstimates(ctx, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.FinancialObjectStorageEstimate(ctx, bucket.AccountID, now); !again.ObservedAt.Equal(now) {
		t.Fatalf("older observation replaced the estimate: %+v", again)
	}
}
