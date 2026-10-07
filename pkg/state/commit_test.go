package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCommitAcceptanceReplayConflictAndConcurrency(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	owner, err := s.CreateAccount(ctx, "commit-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: owner.ID, Slug: "commit-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, account := destination.ID, owner.ID
	src, err := s.CreateCommitSource(ctx, state.CommitSource{AccountID: account, AppID: app, Name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE commit_sources SET relay_status='invented' WHERE id=$1`, src.ID)
	var healthConstraint *pgconn.PgError
	if !errors.As(err, &healthConstraint) || healthConstraint.Code != "23514" {
		t.Fatalf("invalid source health state was accepted: %v", err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account, Slug: "commit-other-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommitSource(ctx, state.CommitSource{AccountID: account, AppID: otherApp.ID, Name: "orders"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("source name reassignment: %v", err)
	}
	event := uuid.NewString()
	inv := state.Invocation{AppID: app, AccountID: account, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", Payload: json.RawMessage(`{"id":"order-1"}`), DueAt: time.Now()}
	receipts := make(chan state.CommitReceipt, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.AcceptCommitEvent(ctx, account, src.ID, event, "order.created", inv.Payload, inv, 1)
			receipts <- r
			failures <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var original state.CommitReceipt
	for r := range receipts {
		if original.ID == "" {
			original = r
		}
		if r.ID != original.ID || r.InvocationID != original.InvocationID {
			t.Fatal("replay created another logical invocation")
		}
	}
	invocations, err := s.ListInvocationsForApp(ctx, app)
	if err != nil || len(invocations) != 1 {
		t.Fatalf("invocations=%d err=%v", len(invocations), err)
	}
	_, err = s.AcceptCommitEvent(ctx, account, src.ID, event, "order.changed", inv.Payload, inv, 1)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed content: %v", err)
	}
	_, err = s.AcceptCommitEvent(ctx, account, src.ID, uuid.NewString(), "order.created", inv.Payload, inv, 1)
	if !errors.Is(err, state.ErrCommitQueueFull) {
		t.Fatalf("capacity: %v", err)
	}
	_, err = s.CommitReceiptByEvent(ctx, uuid.NewString(), src.ID, event)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
	operation, err := s.CommitOperationByID(ctx, account, original.InvocationID)
	if err != nil || operation.State != "accepted" {
		t.Fatalf("accepted operation: %+v %v", operation, err)
	}
	_, err = pool.Exec(ctx, `UPDATE commit_receipts SET operation_state='invented' WHERE id=$1::uuid`, original.ID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23514" {
		t.Fatalf("invalid retained operation state was not rejected by its CHECK: %v", err)
	}
	if _, err := s.ClaimInvocation(ctx, original.InvocationID, "", 30); err != nil {
		t.Fatal(err)
	}
	operation, err = s.CommitOperationByID(ctx, account, original.InvocationID)
	if err != nil || operation.State != "running" {
		t.Fatalf("running operation: %+v %v", operation, err)
	}
	if err := s.CompleteInvocation(ctx, original.InvocationID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	operation, err = s.CommitOperationByID(ctx, account, original.InvocationID)
	if err != nil || operation.State != "completed" || operation.CompletedAt == nil {
		t.Fatalf("completed operation: %+v %v", operation, err)
	}
	if n, err := s.DeleteInvocationsByIDs(ctx, []string{original.InvocationID}); err != nil || n != 1 {
		t.Fatalf("invocation retention: %d %v", n, err)
	}
	retained, err := s.CommitOperationByID(ctx, account, original.InvocationID)
	if err != nil || retained.State != "completed" || retained.CompletedAt == nil {
		t.Fatalf("retained completion: %+v %v", retained, err)
	}
	replayed, err := s.AcceptCommitEvent(ctx, account, src.ID, event, "order.created", inv.Payload, state.Invocation{}, 0)
	if err != nil || replayed.ID != original.ID || replayed.InvocationID != original.InvocationID {
		t.Fatalf("replay after retention: %+v %v", replayed, err)
	}
	remaining, err := s.ListInvocationsForApp(ctx, app)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("retention replay recreated work: %d %v", len(remaining), err)
	}
}
