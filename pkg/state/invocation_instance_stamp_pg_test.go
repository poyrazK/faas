package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 134 — the instance stamp commits without waiting for its WAL flush,
// and only the stamp does: the connection it ran on is back to synchronous
// commit for the next transaction.
func TestPgInstanceStampCommitsAsynchronouslyOnlyItself(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue, DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var stamped bool
	if err := conn.QueryRow(ctx, `select stamp_invocation_instance($1, $2)`, inv.ID, "inst-async").Scan(&stamped); err != nil || !stamped {
		t.Fatalf("stamp = %v, %v", stamped, err)
	}
	var mode string
	if err := conn.QueryRow(ctx, `show synchronous_commit`).Scan(&mode); err != nil || mode != "on" {
		t.Fatalf("synchronous_commit after the stamp = %q, %v; want on", mode, err)
	}
	got, err := s.InvocationByID(ctx, inv.ID)
	if err != nil || got.InstanceID != "inst-async" {
		t.Fatalf("instance = %q, %v; want inst-async", got.InstanceID, err)
	}
}
