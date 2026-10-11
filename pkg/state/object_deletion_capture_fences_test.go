// adr: 590
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func deletionCaptureInput(b state.ObjectBucket) state.ObjectDeletion {
	return state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key"},
		AccountID: b.AccountID, AppID: b.AppID, Token: uuid.NewString()}
}

func TestObjectDeletionCaptureFenceMem(t *testing.T) {
	for _, outcome := range []string{"prepared cancellation", "provider rejection", "completed", "uncertain recovery", "abandoned uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			m := state.NewMemStore()
			now := time.Now().UTC()
			m.SetClockForTest(func() time.Time { return now })
			objectDeletionCaptureFenceContract(t, m, outcome, func() { now = now.Add(api.ObjectDeletionLease + api.ObjectDeletionRetry + time.Second) })
		})
	}
}

func TestObjectDeletionCaptureFencePG(t *testing.T) {
	for _, outcome := range []string{"prepared cancellation", "provider rejection", "completed", "uncertain recovery", "abandoned uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			st, pool, ctx := pgStoreWithPool(t)
			objectDeletionCaptureFenceContract(t, st, outcome, func() {
				if _, err := pool.Exec(ctx, `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE clock_timestamp()-interval '1 second' END,retry_at=clock_timestamp()-interval '1 second' WHERE state IN ('prepared','dispatched')`); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func objectDeletionCaptureFenceContract(t *testing.T, st accountingStore, outcome string, expire func()) {
	t.Helper()
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	d := st.(state.ObjectDeletionStore)
	fences := st.(state.ObjectBucketWriteFenceStore)
	input := deletionCaptureInput(b)
	j, created, err := d.BeginObjectDeletion(ctx, input, accountingPolicy())
	if err != nil || !created {
		t.Fatalf("admit original deletion: %+v %v", j, err)
	}
	fence, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || fence.Deletions != 1 || fence.Requests != 0 || fence.NativeGrants != 0 {
		t.Fatalf("prepared intent missing from capture drainage: %+v %v", fence, err)
	}
	if replay, created, err := d.BeginObjectDeletion(ctx, input, accountingPolicy()); err != nil || created || replay.ID != j.ID || replay.Token != j.Token {
		t.Fatalf("capture hold replaced an original intent: %+v %v", replay, err)
	}
	if _, created, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); !errors.Is(err, state.ErrObjectBucketWriteFenced) || created {
		t.Fatalf("capture admitted another deletion: %v", err)
	}
	other, _ := seedAccounting(t, st)
	if _, created, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(other), accountingPolicy()); err != nil || !created {
		t.Fatalf("capture fenced an unrelated bucket: %v", err)
	}
	assertBusy := func() {
		t.Helper()
		got, err := fences.ReadObjectBucketWriteFence(ctx, b, fence.Token)
		if err != nil || got.Deletions != 1 || got.Requests != 0 || got.NativeGrants != 0 {
			t.Fatalf("unsettled intent disappeared from drainage: %+v %v", got, err)
		}
	}
	if outcome == "prepared cancellation" {
		expire()
		assertBusy()
		j, err = d.ClaimObjectDeletion(ctx, j.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		j.State, j.LastErrorCode = "failed", "preparation_expired"
	} else {
		j, err = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil)
		if err != nil {
			t.Fatalf("capture prevented an admitted intent from dispatching: %v", err)
		}
		assertBusy()
		if outcome == "uncertain recovery" || outcome == "abandoned uncertain" {
			if err := d.RetryObjectDeletion(ctx, j.ID, j.Token, "provider_uncertain"); err != nil {
				t.Fatal(err)
			}
			expire()
			assertBusy()
			if outcome == "abandoned uncertain" {
				if err := fences.ReleaseObjectBucketWriteFence(ctx, b, fence.Token); err != nil {
					t.Fatal(err)
				}
				fence, err = fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				assertBusy()
			}
			j, err = d.ClaimObjectDeletion(ctx, j.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			wrong := j
			wrong.State, wrong.LastErrorCode = "failed", "provider_rejected"
			if _, err := d.FinishObjectDeletion(ctx, wrong); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("recovery rejection settled the original uncertain dispatch: %v", err)
			}
			assertBusy()
		}
		j.State, j.LastErrorCode = "completed", ""
		if outcome == "provider rejection" {
			j.State, j.LastErrorCode = "failed", "provider_rejected"
		}
	}
	if _, err := d.FinishObjectDeletion(ctx, j); err != nil {
		t.Fatalf("capture prevented original settlement: %v", err)
	}
	fence, err = fences.ReadObjectBucketWriteFence(ctx, b, fence.Token)
	if err != nil || fence.Deletions != 0 {
		t.Fatalf("terminal intent remained busy: %+v %v", fence, err)
	}
	if _, _, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatalf("settlement reopened source admission: %v", err)
	}
	if err := fences.ReleaseObjectBucketWriteFence(ctx, b, fence.Token); err != nil {
		t.Fatal(err)
	}
	if _, created, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy()); err != nil || !created {
		t.Fatalf("released capture retained new admission fence: %v", err)
	}
}

func TestMemCloneObjectWriteFenceRetainsDeletionAcrossHandoffAndAbandonment(t *testing.T) {
	cloneObjectWriteFenceDeletionContract(t, state.NewMemStore())
}

func TestPgCloneObjectWriteFenceRetainsDeletionAcrossHandoffAndAbandonment(t *testing.T) {
	s, _ := pgStore(t)
	cloneObjectWriteFenceDeletionContract(t, s)
}

func cloneObjectWriteFenceDeletionContract(t *testing.T, s cloneObjectWriteFenceTestStore) {
	t.Helper()
	ctx := t.Context()
	lease, buckets, _ := cloneObjectWriteFenceFixture(t, s)
	b := buckets[0]
	d := s.(state.ObjectDeletionStore)
	j, _, err := d.BeginObjectDeletion(ctx, deletionCaptureInput(b), accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	j, err = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RetryObjectDeletion(ctx, j.ID, j.Token, "provider_uncertain"); err != nil {
		t.Fatal(err)
	}
	fence, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, lease, b.ID)
	if err != nil || fence.Deletions != 1 {
		t.Fatalf("original deletion not counted by clone: %+v %v", fence, err)
	}
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, lease)
	if err != nil || len(owned) != 1 || owned[0].Deletions != 1 || owned[0].Token != fence.Token {
		t.Fatalf("handoff hid original deletion: %+v %v", owned, err)
	}
	op := lease.Operation
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, lease); err != nil {
		t.Fatal(err)
	}
	retained, err := d.GetObjectDeletion(ctx, b.AccountID, b.ID, j.ID)
	if err != nil || retained.State != "dispatched" || retained.LastErrorCode != "provider_uncertain" {
		t.Fatalf("abandonment settled an unknown provider outcome: %+v %v", retained, err)
	}
	fence, err = s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || fence.Deletions != 1 {
		t.Fatalf("new capture lost original evidence: %+v %v", fence, err)
	}
}
