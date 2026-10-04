//go:build !no_pg

// adr: 581
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectFenceFailureStore struct {
	*state.PgStore
	failBefore, loseAfter bool
}

func (s *cloneObjectFenceFailureStore) AbandonProjectEnvironmentCloneObjectWriteFences(ctx context.Context, lease state.ProjectEnvironmentCloneLease) error {
	if s.failBefore {
		s.failBefore = false
		return errors.New("source barrier cleanup unavailable")
	}
	err := s.PgStore.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, lease)
	if err == nil && s.loseAfter {
		s.loseAfter = false
		return errors.New("source barrier cleanup acknowledgement lost")
	}
	return err
}

func TestCloneObjectWriteFenceCoordinatorAbandonmentRecovers(t *testing.T) {
	fixture := newCloneCoordinatorFixture(t, false)
	srv := fixture.srv
	account, project, app := state.Account{ID: fixture.lease.Operation.AccountID}, state.Project{ID: fixture.lease.Operation.ProjectID}, fixture.apps[0]
	store := &cloneObjectFenceFailureStore{PgStore: fixture.store.PgStore}
	srv.store = store
	ctx := t.Context()
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, fixture.lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	b, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: account.ID, AppID: app.ID, Name: "source", Scope: "production",
		Region: "us-east-1", BackendID: "storage", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "source", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, b.ID, "source", "ready"); err != nil {
		t.Fatal(err)
	}
	b, err = store.GetObjectBucket(ctx, account.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "abandoned", IdempotencyKey: "fenced", SourceRevisionHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	l, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	l.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, l.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, l.Operation.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
		t.Fatal(err)
	}
	l.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, l.Operation.Status, state.CloneOperationCompensating, l.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	store.failBefore = true
	l, err = srv.processProjectEnvironmentCloneLease(ctx, store, l)
	if err == nil {
		t.Fatal("unavailable abandonment advanced cleanup")
	}
	if _, err := store.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
		t.Fatalf("failed cleanup released source: %v", err)
	}
	store.loseAfter = true
	l, err = srv.processProjectEnvironmentCloneLease(ctx, store, l)
	if err == nil {
		t.Fatal("lost cleanup acknowledgement was ignored")
	}
	stale := l
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	l, err = srv.processProjectEnvironmentCloneLease(ctx, store, l)
	if !errors.Is(err, errCloneCompensationUnavailable) || l.Operation.Status != state.CloneOperationCompensating {
		t.Fatalf("abandonment marked full cleanup complete: %+v %v", l, err)
	}
	if _, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker retained authority: %v", err)
	}
	f, err := store.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil || f.Requests != 1 {
		t.Fatalf("abandonment erased unknown writer: %+v %v", f, err)
	}
	if err := store.ReleaseObjectBucketWriteFence(ctx, b, f.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatalf("source remained paused: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "abandoned"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("abandoned capture published a target: %v", err)
	}
}
