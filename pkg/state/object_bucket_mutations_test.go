// adr:567
package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func mutationBucketFixture(t *testing.T, base state.Store) state.ObjectBucket {
	t.Helper()
	ctx := context.Background()
	a, err := base.CreateAccount(ctx, uuid.NewString()+"@mutation.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := base.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "writers", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	buckets := base.(state.ObjectBucketStore)
	b, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: a.ID, AppID: app.ID, Name: "source", Scope: "production", Region: "us-east-1", BackendID: "test", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}, api.MaxObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := buckets.ClaimObjectBucket(ctx, a.ID, app.ID, b.ID, token, "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := buckets.FinishObjectBucket(ctx, b.ID, token, "ready"); err != nil {
		t.Fatal(err)
	}
	b, err = buckets.GetObjectBucket(ctx, a.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestObjectBucketMutationMem(t *testing.T) { objectBucketMutationContract(t, state.NewMemStore()) }
func TestObjectBucketMutationPG(t *testing.T)  { s, _ := pgStore(t); objectBucketMutationContract(t, s) }

func objectBucketMutationContract(t *testing.T, base state.Store) {
	t.Helper()
	ctx := context.Background()
	b := mutationBucketFixture(t, base)
	st := base.(state.ObjectBucketWriteFenceStore)
	receipt, err := st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil || receipt.CreatedAt.IsZero() {
		t.Fatalf("request reservation = %+v, %v", receipt, err)
	}
	grant, err := st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucketMutation(ctx, grant); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("grant finished after signing: %v", err)
	}
	grant.Kind = state.ObjectBucketMutationRequest
	if err := st.FinishObjectBucketMutation(ctx, grant); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("grant disguised as a synchronous writer: %v", err)
	}
	token := uuid.NewString()
	fence, err := st.AcquireObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 1 || fence.NativeGrants != 1 {
		t.Fatalf("fence = %+v, %v", fence, err)
	}
	for _, kind := range []string{state.ObjectBucketMutationRequest, state.ObjectBucketMutationNativeGrant} {
		if _, err := st.BeginObjectBucketMutation(ctx, b, kind); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
			t.Fatalf("fenced %s admitted: %v", kind, err)
		}
	}
	for _, acquire := range []bool{false, true} {
		var err error
		if acquire {
			_, err = st.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
		} else {
			_, err = st.ReadObjectBucketWriteFence(ctx, b, uuid.NewString())
		}
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("another owner adopted fence: %v", err)
		}
	}
	if err := st.ReleaseObjectBucketWriteFence(ctx, b, uuid.NewString()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong owner released source: %v", err)
	}
	tampered := receipt
	tampered.Bucket.PhysicalName += "-replacement"
	if err := st.FinishObjectBucketMutation(ctx, tampered); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed physical placement accepted: %v", err)
	}
	if _, err := base.(state.ObjectBucketStore).ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, uuid.NewString(), "deleting"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("fenced source deleted: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := st.FinishObjectBucketMutation(cancelled, receipt); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request drained: %v", err)
	}
	fence, err = st.ReadObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 1 {
		t.Fatalf("cancellation lost writer: %+v %v", fence, err)
	}
	if err := st.FinishObjectBucketMutation(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	fence, err = st.ReadObjectBucketWriteFence(ctx, b, token)
	if err != nil || fence.Requests != 0 || fence.NativeGrants != 1 {
		t.Fatalf("synchronous observation lost native grant: %+v %v", fence, err)
	}
	if err := st.ReleaseObjectBucketWriteFence(ctx, b, token); err != nil {
		t.Fatal(err)
	}
	// Reopening the source must not erase unknown native activity. A new
	// checkpoint still sees the same grant even after the old owner releases.
	receipt, err = st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectBucketMutation(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	fence, err = st.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || fence.Requests != 0 || fence.NativeGrants != 1 {
		t.Fatalf("reacquisition erased grant: %+v %v", fence, err)
	}
}

func TestObjectBucketMutationRaceMem(t *testing.T) { objectBucketMutationRace(t, state.NewMemStore()) }
func TestObjectBucketMutationRacePG(t *testing.T)  { s, _ := pgStore(t); objectBucketMutationRace(t, s) }

func objectBucketMutationRace(t *testing.T, base state.Store) {
	t.Helper()
	b := mutationBucketFixture(t, base)
	st := base.(state.ObjectBucketWriteFenceStore)
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		token := uuid.NewString()
		start := make(chan struct{})
		var wg sync.WaitGroup
		var receipt state.ObjectBucketMutation
		var beginErr, fenceErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			receipt, beginErr = st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
		}()
		go func() { defer wg.Done(); <-start; _, fenceErr = st.AcquireObjectBucketWriteFence(ctx, b, token) }()
		close(start)
		wg.Wait()
		if fenceErr != nil {
			t.Fatal(fenceErr)
		}
		fence, err := st.ReadObjectBucketWriteFence(ctx, b, token)
		if err != nil {
			t.Fatal(err)
		}
		if beginErr == nil {
			if fence.Requests != 1 {
				t.Fatalf("admitted writer absent from fence: %+v", fence)
			}
			if err := st.FinishObjectBucketMutation(ctx, receipt); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(beginErr, state.ErrObjectBucketWriteFenced) || fence.Requests != 0 {
			t.Fatalf("race = %+v, %v", fence, beginErr)
		}
		if err := st.ReleaseObjectBucketWriteFence(ctx, b, token); err != nil {
			t.Fatal(err)
		}
	}
}
