package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type idempotencyRequestStore interface {
	ReserveIdempotent(ctx context.Context, accountID, key string, abandonAfter time.Duration) (state.IdempotencyReservation, error)
	ReserveIdempotentRequest(ctx context.Context, accountID, key string, fingerprint []byte, abandonAfter time.Duration) (state.IdempotencyReservation, error)
	PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error
}

// testFingerprint builds a fingerprint: a key id, then a MAC of the request.
func testFingerprint(keyID, request string) []byte {
	id, mac := sha256.Sum256([]byte(keyID)), sha256.Sum256([]byte(request))
	return append(id[:state.IdempotencyFingerprintKeyIDSize:state.IdempotencyFingerprintKeyIDSize], mac[:state.IdempotencyFingerprintSize-state.IdempotencyFingerprintKeyIDSize]...)
}

// testReserveIdempotentRequest is the ADR-20261022204036 contract: a key is
// bound to its first request's fingerprint for its replay window, and a key
// stored without one (an earlier release, an unbound caller) or under another
// key id (a rotated key) never conflicts.
func testReserveIdempotentRequest(t *testing.T, ctx context.Context, s idempotencyRequestStore, acctID string) {
	a, b := testFingerprint("key 1", "request a"), testFingerprint("key 1", "request b")
	rotated := testFingerprint("key 2", "request b")
	type step struct {
		name         string
		key          string
		fingerprint  []byte
		abandonAfter time.Duration
		unbound      bool // ReserveIdempotent, as an earlier release calls it
		put          bool // complete the key with 201 "done" instead of reserving
		want         state.IdempotencyReservation
	}
	done := state.IdempotencyReservation{Status: 201, Body: []byte("done")}
	for _, st := range []step{
		{name: "new key", key: "k", fingerprint: a, abandonAfter: time.Hour, want: state.IdempotencyReservation{Reserved: true}},
		{name: "same request in flight waits", key: "k", fingerprint: a, abandonAfter: time.Hour, want: state.IdempotencyReservation{InFlight: true}},
		{name: "other request in flight conflicts", key: "k", fingerprint: b, abandonAfter: time.Hour, want: state.IdempotencyReservation{Conflict: true}},
		{name: "other request cannot take over an abandoned key", key: "k", fingerprint: b, want: state.IdempotencyReservation{Conflict: true}},
		{name: "same request takes over an abandoned key", key: "k", fingerprint: a, want: state.IdempotencyReservation{Reserved: true}},
		{name: "complete", key: "k", put: true},
		{name: "same request replays", key: "k", fingerprint: a, abandonAfter: time.Hour, want: done},
		{name: "other request conflicts", key: "k", fingerprint: b, abandonAfter: time.Hour, want: state.IdempotencyReservation{Conflict: true}},
		{name: "unfingerprinted request replays", key: "k", abandonAfter: time.Hour, want: done},
		{name: "request under a rotated key replays", key: "k", fingerprint: rotated, abandonAfter: time.Hour, want: done},

		{name: "reservation under the old key", key: "rotation", fingerprint: a, abandonAfter: time.Hour, want: state.IdempotencyReservation{Reserved: true}},
		{name: "rotated key waits for it in flight", key: "rotation", fingerprint: rotated, abandonAfter: time.Hour, want: state.IdempotencyReservation{InFlight: true}},
		{name: "rotated key takes over the abandoned key", key: "rotation", fingerprint: rotated, want: state.IdempotencyReservation{Reserved: true}},
		{name: "and binds it under the new key", key: "rotation", fingerprint: testFingerprint("key 2", "request c"), abandonAfter: time.Hour, want: state.IdempotencyReservation{Conflict: true}},

		{name: "receipt stored without a fingerprint", key: "legacy", put: true},
		{name: "any request replays an unbound receipt", key: "legacy", fingerprint: b, abandonAfter: time.Hour, want: done},

		{name: "bound reservation", key: "skew", fingerprint: a, abandonAfter: time.Hour, want: state.IdempotencyReservation{Reserved: true}},
		{name: "earlier release takes over the abandoned key", key: "skew", unbound: true, want: state.IdempotencyReservation{Reserved: true}},
		{name: "earlier release completes it", key: "skew", put: true},
		{name: "its receipt is unbound", key: "skew", fingerprint: b, abandonAfter: time.Hour, want: done},
	} {
		if st.put {
			if err := s.PutIdempotent(ctx, acctID, st.key, 201, []byte("done")); err != nil {
				t.Fatalf("%s: %v", st.name, err)
			}
			continue
		}
		var got state.IdempotencyReservation
		var err error
		if st.unbound {
			got, err = s.ReserveIdempotent(ctx, acctID, st.key, st.abandonAfter)
		} else {
			got, err = s.ReserveIdempotentRequest(ctx, acctID, st.key, st.fingerprint, st.abandonAfter)
		}
		if err != nil || got.Reserved != st.want.Reserved || got.InFlight != st.want.InFlight || got.Conflict != st.want.Conflict ||
			got.Status != st.want.Status || !bytes.Equal(got.Body, st.want.Body) {
			t.Fatalf("%s: reserve = %+v err=%v, want %+v", st.name, got, err, st.want)
		}
	}
	if _, err := s.ReserveIdempotentRequest(ctx, acctID, "short", []byte("not-a-digest"), time.Hour); err == nil {
		t.Fatal("a fingerprint that is not SHA-256 was accepted")
	}
}

func TestMem_ReserveIdempotentRequest(t *testing.T) {
	testReserveIdempotentRequest(t, t.Context(), state.NewMemStore(), "acct")
}

func TestPg_ReserveIdempotentRequest(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)
	testReserveIdempotentRequest(t, ctx, s, acctID)

	// After its 24 h window a key may be bound to a new request.
	if _, err := pool.Exec(ctx, `update idempotency_keys set created_at = now() - interval '25 hours' where account_id = $1 and key = 'k'`, acctID); err != nil {
		t.Fatal(err)
	}
	other := testFingerprint("key 1", "request b")
	if res, err := s.ReserveIdempotentRequest(ctx, acctID, "k", other, time.Hour); err != nil || !res.Reserved {
		t.Fatalf("reserve after the window = %+v err=%v, want the key rebound", res, err)
	}
	var stored []byte
	if err := pool.QueryRow(ctx, `select request_digest from idempotency_keys where account_id = $1 and key = 'k'`, acctID).Scan(&stored); err != nil || !bytes.Equal(stored, other) {
		t.Fatalf("stored fingerprint = %x err=%v, want %x", stored, err, other)
	}
}
