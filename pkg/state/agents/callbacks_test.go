// adr: 20261030125501 §3 — a thread's latest revocation is what apid checks a
// callback token's issue time against, and both are read from the database's
// clock. §4 — a gregale_run run is linked to its thread in the admitting
// transaction and settled once.
package agents_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestCallbackRevocations(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	const endpoint = "33333333-3333-3333-3333-333333333333"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.CallbacksRevokedAt(ctx, endpoint, "thread-1"); err != nil || found {
		t.Fatalf("an unrevoked thread: %v %v", found, err)
	}
	first, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || first.IsZero() {
		t.Fatalf("revoke: %v %v", first, err)
	}
	if at, found, err := s.CallbacksRevokedAt(ctx, endpoint, "thread-1"); err != nil || !found || !at.Equal(first) {
		t.Fatalf("revoked at %v %v %v, want %v", at, found, err, first)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || !second.After(first) {
		t.Fatalf("a second revocation %v is not later than %v: %v", second, first, err)
	}
	// apid stamps a token's issue time from CallbackClock, so a revocation
	// and an issue order on the one database clock.
	issued, err := s.CallbackClock(ctx)
	if err != nil || !issued.After(second) {
		t.Fatalf("the clock read after a revocation, %v, is not later than it, %v: %v", issued, second, err)
	}
	third, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || !third.After(issued) {
		t.Fatalf("a revocation after a clock read, %v, is not later than it, %v: %v", third, issued, err)
	}
	if _, found, _ := s.CallbacksRevokedAt(ctx, endpoint, "thread-2"); found {
		t.Fatal("another thread reads as revoked")
	}
	if _, err := s.RevokeCallbacks(ctx, account, endpoint, ""); err == nil {
		t.Fatal("an empty thread was revoked")
	}
}

func TestRunCallbacks(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	const endpoint = "44444444-4444-4444-4444-444444444444"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAgent(ctx, account, "triage", "", spec(), 10); err != nil {
		t.Fatal(err)
	}
	issued := time.Now().UTC().Truncate(time.Microsecond)
	link := &agents.RunCallback{EndpointID: endpoint, ThreadID: "thread-1", ToolCallID: "call_1", ProviderCallID: "prov-1", IssuedAt: issued}
	run := admit(t, s, agents.Admission{DedupeKey: "gregale-run-1", Callback: link})
	retried := admit(t, s, agents.Admission{DedupeKey: "gregale-run-1", Callback: &agents.RunCallback{EndpointID: endpoint,
		ThreadID: "thread-1", ToolCallID: "call_other", IssuedAt: issued}})
	plain := admit(t, s, agents.Admission{})
	if retried.ID != run.ID {
		t.Fatalf("a retried call admitted a second run: %s, %s", run.ID, retried.ID)
	}
	if ends, err := s.UndeliveredRunCallbacks(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("an unfinished run is undelivered: %+v %v", ends, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_runs SET state='succeeded', finished_at=now() WHERE id = ANY($1)`, []string{run.ID, plain.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_run_outputs (run_id, account_id, result) VALUES ($1, $2, '{"ok": true}')`, run.ID, account); err != nil {
		t.Fatal(err)
	}
	ends, err := s.UndeliveredRunCallbacks(ctx, 10)
	if err != nil || len(ends) != 1 {
		t.Fatalf("undelivered %+v %v; want only the linked run", ends, err)
	}
	end := ends[0]
	if end.RunID != run.ID || end.AccountID != account || end.State != agents.StateSucceeded || end.Callback != *link {
		t.Fatalf("end %+v, want the first call's link %+v", end, *link)
	}
	var result map[string]bool
	if json.Unmarshal(end.Result, &result) != nil || !result["ok"] {
		t.Fatalf("result %s", end.Result)
	}
	if err := s.MarkRunCallbackDelivered(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredRunCallbacks(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a delivered run is listed again: %+v %v", ends, err)
	}
}
