// adr: 583
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectWriteFenceTestStore interface {
	cloneBindingTestStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneObjectWriteFenceStore
	state.ObjectBucketWriteFenceStore
}

func cloneObjectWriteFenceFixture(t *testing.T, s cloneObjectWriteFenceTestStore) (state.ProjectEnvironmentCloneLease, []state.ObjectBucket, state.ObjectBucket) {
	t.Helper()
	a, p, app, op := cloneBindingFixture(t, s)
	buckets := []state.ObjectBucket{cloneBindingBucket(t, s, a, app, "first", "production", true),
		cloneBindingBucket(t, s, a, app, "second", "production", true)}
	foreign := cloneBindingBucket(t, s, a, app, "foreign", "other", true)
	l, err := s.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil || l.Operation.ID != op.ID {
		t.Fatalf("claim: %+v %v", l, err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(t.Context(), a.ID, p.ID, op.ID, l.Operation.Revision); err != nil {
		t.Fatal(err)
	}
	return l, buckets, foreign
}

func TestMemCloneObjectWriteFenceOwnershipAndRecovery(t *testing.T) {
	cloneObjectWriteFenceContract(t, state.NewMemStore())
}

func cloneObjectWriteFenceContract(t *testing.T, s cloneObjectWriteFenceTestStore) {
	t.Helper()
	ctx := t.Context()
	l, buckets, foreign := cloneObjectWriteFenceFixture(t, s)
	b := buckets[0]
	for _, fault := range []string{"token", "revision", "status", "account", "project", "foreign_source", "unknown_source"} {
		bad, sourceID := l, b.ID
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision--
		case "status":
			bad.Operation.Status = state.CloneOperationCopying
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		case "foreign_source":
			sourceID = foreign.ID
		case "unknown_source":
			sourceID = uuid.NewString()
		}
		if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, bad, sourceID); err == nil {
			t.Fatalf("accepted %s", fault)
		}
	}
	// Even an identical token does not let a clone adopt another owner's fence.
	if _, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.MustParse(l.Operation.ID).String()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("adopted generic fence: %v", err)
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, uuid.MustParse(l.Operation.ID).String()); err != nil {
		t.Fatal(err)
	}
	request, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant); err != nil {
		t.Fatal(err)
	}
	for i, bucket := range buckets {
		f, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, bucket.ID)
		if err != nil || f.CloneOperationID != l.Operation.ID || f.Token != uuid.MustParse(l.Operation.ID).String() || f.BucketID != bucket.ID {
			t.Fatalf("clone ownership: %+v %v", f, err)
		}
		if i == 0 && (f.Requests != 1 || f.NativeGrants != 1) {
			t.Fatalf("lost writers: %+v", f)
		}
		if _, err := s.BeginObjectBucketMutation(ctx, bucket, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
			t.Fatalf("admitted fenced source: %v", err)
		}
		if err := s.ReleaseObjectBucketWriteFence(ctx, bucket, uuid.MustParse(l.Operation.ID).String()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("generic seam released clone barrier: %v", err)
		}
		if _, err := s.AcquireObjectBucketWriteFence(ctx, bucket, uuid.MustParse(l.Operation.ID).String()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("generic seam adopted clone barrier: %v", err)
		}
		if _, err := s.ClaimObjectBucket(ctx, bucket.AccountID, bucket.AppID, bucket.ID, "delete", "deleting"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("deleted fenced source: %v", err)
		}
	}
	if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capturing owner resumed writes: %v", err)
	}
	for _, next := range []string{state.CloneOperationCopying, state.CloneOperationFailed} {
		op := l.Operation
		if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, next, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s abandoned barrier ownership: %v", next, err)
		}
	}
	stale := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker read owner fences: %v", err)
	}
	if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, stale, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker acquired barrier: %v", err)
	}
	f, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID)
	if err != nil || f.Requests != 1 || f.NativeGrants != 1 || f.CloneOperationID != stale.Operation.ID {
		t.Fatalf("takeover lost ownership/activity: %+v %v", f, err)
	}
	if err := s.FinishObjectBucketMutation(ctx, request); err != nil {
		t.Fatal(err)
	}
	op := l.Operation
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, l.Operation.Status, state.CloneOperationCompensated, l.Operation.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("terminal cleanup lost source fence: %v", err)
	}
	if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker abandoned barriers: %v", err)
	}
	foreignToken := uuid.NewString()
	if _, err := s.AcquireObjectBucketWriteFence(ctx, foreign, foreignToken); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(cancelled, l); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled abandonment: %v", err)
	}
	fences, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l)
	if err != nil || len(fences) != 2 {
		t.Fatalf("lost source fences before cleanup: %+v %v", fences, err)
	}
	for range 2 {
		if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if fences, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l); err != nil || len(fences) != 0 {
		t.Fatalf("cleanup replay: %+v %v", fences, err)
	}
	if _, err := s.ReadObjectBucketWriteFence(ctx, foreign, foreignToken); err != nil {
		t.Fatalf("cleanup released another owner: %v", err)
	}
	f, err = s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Requests != 0 || f.NativeGrants != 1 {
		t.Fatalf("abandonment drained native grant: %+v %v", f, err)
	}
	if err := s.ReleaseObjectBucketWriteFence(ctx, b, f.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatalf("source did not reopen: %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, l.Operation.Status, state.CloneOperationCompensated, l.Operation.Revision, nil, ""); err != nil {
		t.Fatalf("unfenced compensated operation: %v", err)
	}
}
