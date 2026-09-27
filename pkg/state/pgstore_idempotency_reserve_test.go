package state_test

import (
	"testing"
	"time"
)

// TestPg_ReserveIdempotent covers the reservation lifecycle apid's
// idempotent middleware relies on: reserve → in-flight → complete → replay,
// in-flight rows are invisible to GetIdempotent, and an abandoned
// reservation is taken over.
func TestPg_ReserveIdempotent(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)

	res, err := s.ReserveIdempotent(ctx, acctID, "POST /v1/x\nk1", time.Hour)
	if err != nil || !res.Reserved {
		t.Fatalf("first reserve = %+v err=%v, want reserved", res, err)
	}
	res, err = s.ReserveIdempotent(ctx, acctID, "POST /v1/x\nk1", time.Hour)
	if err != nil || !res.InFlight {
		t.Fatalf("second reserve = %+v err=%v, want in-flight", res, err)
	}
	if _, _, err := s.GetIdempotent(ctx, acctID, "POST /v1/x\nk1"); err == nil {
		t.Fatal("GetIdempotent returned an in-flight reservation")
	}
	if err := s.PutIdempotent(ctx, acctID, "POST /v1/x\nk1", 201, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	res, err = s.ReserveIdempotent(ctx, acctID, "POST /v1/x\nk1", time.Hour)
	if err != nil || res.Reserved || res.InFlight || res.Status != 201 || string(res.Body) != `{"ok":true}` {
		t.Fatalf("reserve after completion = %+v err=%v, want a 201 replay", res, err)
	}

	if res, err := s.ReserveIdempotent(ctx, acctID, "POST /v1/x\nk2", time.Hour); err != nil || !res.Reserved {
		t.Fatalf("reserve k2 = %+v err=%v", res, err)
	}
	if res, err := s.ReserveIdempotent(ctx, acctID, "POST /v1/x\nk2", 0); err != nil || !res.Reserved {
		t.Fatalf("reserve past abandonAfter = %+v err=%v, want the abandoned key taken over", res, err)
	}
}

// TestPg_PutIdempotentRefreshesReplayWindow — the upsert refreshed status
// and body but not created_at, and GetIdempotent only reads rows younger
// than 24 h. A key reused after a day therefore never replayed again: every
// retry re-executed. (MemStore always refreshed; PgStore diverged.)
func TestPg_PutIdempotentRefreshesReplayWindow(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	if err := s.PutIdempotent(ctx, acctID, "reused", 201, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update idempotency_keys set created_at = now() - interval '25 hours' where account_id = $1`, acctID); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIdempotent(ctx, acctID, "reused", 202, []byte("second")); err != nil {
		t.Fatal(err)
	}
	status, body, err := s.GetIdempotent(ctx, acctID, "reused")
	if err != nil || status != 202 || string(body) != "second" {
		t.Fatalf("GetIdempotent after reuse = (%d, %q, %v), want the fresh 202 response", status, body, err)
	}
}
