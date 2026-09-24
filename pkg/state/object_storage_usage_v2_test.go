package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type customerUsageV2Store interface {
	accountingStore
	state.ObjectStorageCustomerUsageV2Store
}

func TestObjectCustomerUsageV2Mem(t *testing.T) { objectCustomerUsageV2Suite(t, state.NewMemStore()) }
func TestObjectCustomerUsageV2PG(t *testing.T) {
	st, _ := pgStore(t)
	objectCustomerUsageV2Suite(t, st)
}

func objectCustomerUsageV2Suite(t *testing.T, st customerUsageV2Store) {
	// adr: 237 — v2 is shadow-only; it cannot replace the live v1 safety report.
	ctx := context.Background()
	bucket, _ := seedAccounting(t, st)
	now := time.Now().UTC().Truncate(time.Microsecond)
	period := state.ObjectStoragePeriod(now)
	unknown := api.ObjectStorageCustomerUsageReportV2{
		AccountID: bucket.AccountID, BackendID: bucket.BackendID,
		BackendFingerprint: bucket.BackendFingerprint, Source: "provider-evidence",
		Version: api.ObjectStorageCustomerUsageVersion, PeriodStart: period,
		CoverageEnd: period, ObservedAt: now.Add(-time.Second),
		EvidenceDigest: strings.Repeat("a", 64), StoredByteHours: 100,
		ReadOperations: 2, WriteOperations: 3,
	}
	before, err := st.ObjectUsage(ctx, bucket.AccountID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordObjectCustomerUsageV2(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordObjectCustomerUsageV2(ctx, unknown); err != nil {
		t.Fatal("idempotent replay", err)
	}
	got, err := st.LatestObjectCustomerUsageV2(ctx, bucket.AccountID, bucket.BackendID, period)
	if err != nil || got.EgressBytes != nil || got.ReadOperations != 2 {
		t.Fatalf("latest: %+v, %v", got, err)
	}
	conflict := unknown
	conflict.ReadOperations++
	if err := st.RecordObjectCustomerUsageV2(ctx, conflict); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting replay = %v", err)
	}
	value := int64(5)
	next := unknown
	next.CoverageEnd = now.Add(-time.Second)
	next.ObservedAt = now
	next.EvidenceDigest = strings.Repeat("b", 64)
	next.ReadOperations++
	next.EgressBytes = &value
	if err := st.RecordObjectCustomerUsageV2(ctx, next); err != nil {
		t.Fatal(err)
	}
	value = 99 // caller mutation must not change a stored report
	got, err = st.LatestObjectCustomerUsageV2(ctx, bucket.AccountID, bucket.BackendID, period)
	if err != nil || got.EgressBytes == nil || *got.EgressBytes != 5 || got.ReadOperations != 3 {
		t.Fatalf("latest with egress: %+v, %v", got, err)
	}
	regressed := next
	regressed.ObservedAt = time.Now().UTC().Truncate(time.Microsecond)
	regressed.CoverageEnd = now
	regressed.ReadOperations = 1
	if err := st.RecordObjectCustomerUsageV2(ctx, regressed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted regression: %v", err)
	}
	missingEgress := regressed
	missingEgress.ReadOperations = 4
	missingEgress.EgressBytes = nil
	if err := st.RecordObjectCustomerUsageV2(ctx, missingEgress); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted loss of egress evidence: %v", err)
	}
	wrongPlacement := unknown
	wrongPlacement.BackendID = "other-backend"
	if err := st.RecordObjectCustomerUsageV2(ctx, wrongPlacement); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("accepted wrong placement: %v", err)
	}
	fingerprintConflict := unknown
	fingerprintConflict.BackendFingerprint = strings.Repeat("c", 64)
	if err := st.RecordObjectCustomerUsageV2(ctx, fingerprintConflict); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted conflicting replay: %v", err)
	}
	invalid := unknown
	invalid.Version = 3
	if err := st.RecordObjectCustomerUsageV2(ctx, invalid); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted wrong version: %v", err)
	}
	after, err := st.ObjectUsage(ctx, bucket.AccountID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Reports) != len(before.Reports) || after.Reports[0] != before.Reports[0] {
		t.Fatal("v2 changed live admission report")
	}
	bare := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, bare.AccountID, bare.AppID, bare.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, bare.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, bare.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, bare.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	onlyV2 := unknown
	onlyV2.AccountID, onlyV2.BackendID, onlyV2.BackendFingerprint = bare.AccountID, bare.BackendID, bare.BackendFingerprint
	onlyV2.CoverageEnd = time.Now().UTC().Truncate(time.Microsecond)
	onlyV2.ObservedAt = onlyV2.CoverageEnd
	if err := st.RecordObjectCustomerUsageV2(ctx, onlyV2); err != nil {
		t.Fatal(err)
	}
	if err := st.AdmitObjectURL(ctx, bare.AccountID, bare.ID, "file", 1, true, accountingPolicy()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("v2 bypassed missing v1 safety report: %v", err)
	}
}
