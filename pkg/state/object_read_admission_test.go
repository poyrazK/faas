// adr: 20261030075301

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectReadAdmissionMem(t *testing.T) { objectReadAdmissionSuite(t, state.NewMemStore()) }
func TestObjectReadAdmissionPG(t *testing.T) {
	st, _ := pgStore(t)
	b := objectReadAdmissionSuite(t, st)
	// The owner check is the locking admission's: an abuse hold stops reads.
	ctx := context.Background()
	if _, err := st.SetAccountAbuseHold(ctx, b.AccountID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
		t.Fatal(err)
	}
	d, err := st.ObjectReadAdmission(ctx, b.AccountID, b.ID, time.Now(), gatewaySafetyPolicy())
	if err != nil || !errors.Is(d.Err, state.ErrNotFound) {
		t.Fatalf("held owner's decision = %+v, %v", d, err)
	}
	if err = st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, gatewaySafetyPolicy()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("locking admission disagrees for a held owner: %v", err)
	}
}

func objectReadAdmissionSuite(t *testing.T, st contractTestStore) state.ObjectBucket {
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
	now := time.Now().UTC()
	decide := func() state.ObjectReadAdmission {
		t.Helper()
		d, err := st.ObjectReadAdmission(ctx, b.AccountID, b.ID, time.Now(), p)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := decide(); !errors.Is(d.Err, state.ErrObjectUsageStale) {
		t.Fatalf("uninventoried bucket's decision = %+v", d)
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	d := decide()
	if d.Err != nil || d.Requests != 0 || d.EgressBytes != 0 || d.Authorizations != 0 || d.MaxRequests != p.MaxMonthlyRequests || d.MaxEgressBytes != p.MaxMonthlyEgressBytes || d.MaxAuthorizations != p.MaxMonthlyAuthorizations || !d.Period.Equal(state.ObjectStoragePeriod(now)) {
		t.Fatalf("admitting decision = %+v", d)
	}
	if other, err := st.ObjectReadAdmission(ctx, uuid.NewString(), b.ID, time.Now(), p); err != nil || !errors.Is(other.Err, state.ErrNotFound) {
		t.Fatalf("another account's decision = %+v, %v", other, err)
	}

	period := state.ObjectStoragePeriod(now)
	usage := []state.ObjectReadUsage{
		{BucketID: b.ID, Period: period, Requests: 3, Authorizations: 2, EgressBytes: 40},
		{BucketID: uuid.NewString(), Period: period, Requests: 5, Authorizations: 5, EgressBytes: 5},
	}
	if err := st.RecordObjectReadUsage(ctx, usage); err != nil {
		t.Fatal("a deleted bucket's counts failed the flush", err)
	}
	if d = decide(); d.Err != nil || d.Requests != 3 || d.Authorizations != 2 || d.EgressBytes != 40 {
		t.Fatalf("decision after a flush = %+v", d)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, p); err != nil {
		t.Fatal(err)
	}
	if d = decide(); d.Authorizations != 3 {
		t.Fatalf("locking and batched authorizations do not share a counter: %+v", d)
	}
	if err := st.RecordObjectReadUsage(ctx, []state.ObjectReadUsage{{BucketID: b.ID, Period: period, Requests: p.MaxMonthlyRequests - 3}}); err != nil {
		t.Fatal(err)
	}
	var limit *state.ObjectStorageLimitError
	if d = decide(); !errors.As(d.Err, &limit) || limit.Kind != "requests" || !errors.Is(d.Err, state.ErrObjectBudget) {
		t.Fatalf("decision at the request ceiling = %+v", d)
	}
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "key", 0, false, p); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("locking admission disagrees at the ceiling: %v", err)
	}
	for _, bad := range [][]state.ObjectReadUsage{
		{{BucketID: b.ID, Period: period, Requests: -1}},
		{{BucketID: b.ID, Period: period.Add(time.Hour), Requests: 1}},
		{{Period: period, Requests: 1}},
	} {
		if err := st.RecordObjectReadUsage(ctx, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("invalid usage %+v = %v", bad, err)
		}
	}
	return b
}

// Gateways flush overlapping buckets and owners at once; ordered writes keep
// them from deadlocking and every count lands.
func TestObjectReadUsageConcurrentFlushPG(t *testing.T) {
	st, _ := pgStore(t)
	ctx := context.Background()
	first, second := seedRecoveryBucket(t, st), seedRecoveryBucket(t, st)
	period := state.ObjectStoragePeriod(time.Now())
	const flushes = 16
	errs := make(chan error, flushes)
	for i := range flushes {
		batch := []state.ObjectReadUsage{{BucketID: first.ID, Period: period, Requests: 1, Authorizations: 1}, {BucketID: second.ID, Period: period, Requests: 1, Authorizations: 1}}
		if i%2 == 1 {
			batch[0], batch[1] = batch[1], batch[0]
		}
		go func() { errs <- st.RecordObjectReadUsage(ctx, batch) }()
	}
	for range flushes {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []state.ObjectBucket{first, second} {
		snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if u := state.SummarizeObjectUsage(snapshot, gatewaySafetyPolicy(), time.Now()); u.RequestCount != flushes || u.Authorizations != flushes {
			t.Fatalf("bucket %s counted %d requests and %d authorizations; want %d", b.ID, u.RequestCount, u.Authorizations, flushes)
		}
	}
}
