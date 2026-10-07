// adr: 590
package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestObjectMultipartPartWritersMem(t *testing.T) {
	multipartPartWriterSuite(t, state.NewMemStore())
}
func TestObjectMultipartPartWritersPG(t *testing.T) {
	st, _ := pgStore(t)
	multipartPartWriterSuite(t, st)
}

func multipartPartWriterSuite(t *testing.T, st accountingStore) {
	ctx := context.Background()
	b, u := activeTrackedUpload(t, st, "bound-part")
	transfers := st.(state.ObjectMultipartTransferStore)
	writers := st.(state.ObjectMultipartPartMutationStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	unrelated, err := fences.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := transfers.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "new", 2, 30, 100, accountingPolicy()); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatal("hold admitted fresh transfer", err)
	}
	// Resume only already-admitted authority; the hold prevents new transfer admission.
	r, err := writers.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writers.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replayed dispatch", err)
	}
	if err = fences.FinishObjectBucketMutation(ctx, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic finish erased bound receipt", err)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unproven transfer settlement", err)
	}
	wrong := r
	wrong.Bucket.PhysicalName += "-other"
	if err = writers.FinishObjectMultipartPartMutation(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong placement settled part", err)
	}
	observed, err := fences.ReadObjectBucketWriteFence(ctx, b, hold.Token)
	if err != nil || observed.Requests != 3 {
		t.Fatal("lost independent custody", observed, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = writers.FinishObjectMultipartPartMutation(cancelled, r); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled transaction committed", err)
	}
	if err = writers.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = writers.FinishObjectMultipartPartMutation(ctx, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("duplicate receipt settlement", err)
	}
	if _, err = writers.DispatchObjectMultipartPartMutation(ctx, b, u.ID, 1, "original"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("settled token replayed", err)
	}
	observed, err = fences.ReadObjectBucketWriteFence(ctx, b, hold.Token)
	if err != nil || observed.Requests != 2 {
		t.Fatal("settlement erased parent or unrelated writer", observed, err)
	}
	claimed, err := st.(state.ObjectMultipartUploadStore).ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := transfers.ObjectMultipartAbortReady(ctx, u.ID, claimed.LeaseToken); err != nil || !ready {
		t.Fatal("atomic settlement retained transfer", ready, err)
	}
	if err = transfers.FinishVerifiedObjectMultipartAbort(ctx, u.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	observed, err = fences.ReadObjectBucketWriteFence(ctx, b, hold.Token)
	if err != nil || observed.Requests != 1 || observed.Multipart != 0 {
		t.Fatal("parent settlement lost unrelated receipt", observed, err)
	}
	if err = fences.FinishObjectBucketMutation(ctx, unrelated); err != nil {
		t.Fatal(err)
	}
}

func TestObjectMultipartPartWriterUnknownSurvivesDeadlineMem(t *testing.T) {
	st := state.NewMemStore()
	b, u := activeTrackedUpload(t, st, "unknown-part")
	now := time.Now()
	st.SetClockForTest(func() time.Time { return now })
	if err := st.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, "unknown", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	r, err := st.DispatchObjectMultipartPartMutation(t.Context(), b, u.ID, 1, "unknown")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(45 * time.Minute)
	if !u.ExpiresAt.After(now) {
		t.Fatal("fixture expired the parent instead of its transfer")
	}
	if err = st.BeginObjectMultipartPart(t.Context(), b.AccountID, b.ID, u.ID, "replacement", 1, 30, 100, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("deadline replaced uncertain attempt", err)
	}
	if err = st.SettleObjectMultipartPart(t.Context(), b.AccountID, u.ID, 1, "unknown"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("deadline proved settlement", err)
	}
	claimed, err := st.ClaimObjectMultipartUpload(t.Context(), b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := st.ObjectMultipartAbortReady(t.Context(), u.ID, claimed.LeaseToken); err != nil || ready {
		t.Fatal("deadline authorized cleanup", ready, err)
	}
	if err = st.FinishVerifiedObjectMultipartAbort(t.Context(), u.ID, claimed.LeaseToken); !errors.Is(err, state.ErrConflict) {
		t.Fatal("parent erased unknown writer", err)
	}
	// A late, positively validated reply still belongs to its original token.
	if err = st.FinishObjectMultipartPartMutation(t.Context(), r); err != nil {
		t.Fatal("late original proof rejected", err)
	}
	if ready, err := st.ObjectMultipartAbortReady(t.Context(), u.ID, claimed.LeaseToken); err != nil || !ready {
		t.Fatal(ready, err)
	}
}
