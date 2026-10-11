package state_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type gatewayMeterStore interface {
	accountingStore
	state.ObjectStorageProviderUsageStore
	state.ObjectStorageProviderEgressStore
	state.ObjectStorageGatewayEgressStore
	state.ObjectStorageGatewayRequestStore
}

func gatewaySafetyPolicy() api.ObjectStoragePolicy {
	p := accountingPolicy()
	p.AccountingMode = api.ObjectStorageGatewaySafetyV1
	p.MaxMonthlyCostMillicents = 0
	since := time.Now().UTC().Add(-time.Minute)
	p.GatewayMeteringSince = &since
	return p
}

func TestGatewaySafetyAccountingMem(t *testing.T) {
	gatewaySafetyAccountingSuite(t, state.NewMemStore())
}

func TestGatewaySafetyAccountingPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	b, p := gatewaySafetyAccountingSuite(t, st)
	// A replacement process reads the same durable counters, without any
	// provider report or exporter restoring an in-memory accumulator.
	snapshot, err := state.NewPgStore(pool).ObjectUsage(t.Context(), b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u := state.SummarizeObjectUsage(snapshot, p, time.Now()); !u.Fresh || u.RequestCount != 2 || u.EgressBytes != 100 {
		t.Fatal("replacement store lost usage", u)
	}
}

func gatewaySafetyAccountingSuite(t *testing.T, st gatewayMeterStore) (state.ObjectBucket, api.ObjectStoragePolicy) {
	t.Helper()
	ctx := context.Background()
	b := seedRecoveryBucket(t, st)
	if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	p := gatewaySafetyPolicy()
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, p); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatal("missing inventory admitted", err)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 10, true, p); err != nil {
		t.Fatal("gateway admission required a provider report", err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, accountingPolicy()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatal("legacy report requirement weakened", err)
	}
	if err := st.RecordObjectStorageProviderRequest(ctx, b.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordObjectStorageProviderEgress(ctx, b.ID, 42, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u := state.SummarizeObjectUsage(snapshot, p, time.Now())
	if !u.Fresh || u.RequestCount != 1 || u.EgressBytes != 42 || u.CapacityBytes != 10 || len(u.UnavailableMeters) != 2 || len(snapshot.Reports) != 0 {
		t.Fatal("durable meters not used for admission", u)
	}
	limited := p
	limited.MaxMonthlyRequests = 1
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, limited); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatal("request budget bypass", err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	limited.MaxMonthlyRequests = 2
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := st.ReserveObjectStorageGatewayRequest(ctx, b.ID, time.Now(), limited); err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectBudget) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("concurrent request reservations overspent account", winners.Load())
	}
	winners.Store(0)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.ReserveObjectStorageGatewayEgress(ctx, b.ID, 10, time.Now(), p)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, state.ErrObjectBudget) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 5 {
		t.Fatal("concurrent egress overspent account", winners.Load())
	}
	if err := st.ReserveObjectStorageGatewayEgress(ctx, b.ID, 8, time.Now(), p); err != nil {
		t.Fatal(err)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, p); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatal("egress budget did not stop URL issuance", err)
	}
	return b, p
}

func TestGatewaySafetyRejectsHistoricalAndIncompleteCoverage(t *testing.T) {
	now := time.Now().UTC()
	p := gatewaySafetyPolicy()
	b := state.ObjectBucketUsage{Bucket: state.ObjectBucket{ID: "bucket", State: "ready", CreatedAt: now.Add(-time.Second)}, ObservedAt: now, GatewayMetricsKnown: true}
	for _, tc := range []struct {
		name   string
		mutate func(*state.ObjectBucketUsage)
	}{
		{"historical bucket", func(b *state.ObjectBucketUsage) { b.Bucket.CreatedAt = p.GatewayMeteringSince.Add(-time.Second) }},
		{"unknown creation", func(b *state.ObjectBucketUsage) { b.Bucket.CreatedAt = time.Time{} }},
		{"missing ledger", func(b *state.ObjectBucketUsage) { b.GatewayMetricsKnown = false }},
		{"stale inventory", func(b *state.ObjectBucketUsage) { b.ObservedAt = now.Add(-time.Hour) }},
		{"future inventory", func(b *state.ObjectBucketUsage) { b.ObservedAt = now.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := b
			tc.mutate(&candidate)
			u := state.SummarizeObjectUsage(state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{candidate}}, p, now)
			if u.Fresh {
				t.Fatal("incomplete coverage admitted", u)
			}
		})
	}
	// Deletion keeps this month's consumed budgets; next month's counters
	// have a distinct key and still require a fresh capacity observation.
	b.Bucket.State, b.Bucket.UpdatedAt = "deleted", now
	b.RequestCount, b.EgressBytes = 7, 42
	u := state.SummarizeObjectUsage(state.ObjectUsageSnapshot{Buckets: []state.ObjectBucketUsage{b}}, p, now)
	if !u.Fresh || u.RequestCount != 7 || u.EgressBytes != 42 {
		t.Fatal("bucket deletion erased usage", u)
	}
}
