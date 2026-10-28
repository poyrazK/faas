package gateway

import (
	"bytes"
	"errors"
	"testing"
)

// adr: 091 — amendment 2026-10-28 (review round 3): kind=validate holds a
// body in memory to check it, so the bodies one gateway holds at once are
// bounded. A body the budget cannot hold is refused before it is read; the
// bytes come back when the request lets go of the body.
func TestValidateBodyBudget(t *testing.T) {
	const budget = 100 << 10
	b := newValidateBodyBudget(budget)
	first := bytes.Repeat([]byte("a"), 80<<10)
	got, held, err := b.read(bytes.NewReader(first), int64(len(first)))
	if err != nil || !bytes.Equal(got, first) || held != int64(len(first)) {
		t.Fatalf("first read = %d bytes, held %d, err %v", len(got), held, err)
	}
	if _, _, err := b.read(bytes.NewReader(first), int64(len(first))); !errors.Is(err, errValidateBodyBudget) {
		t.Fatalf("second declared body = %v, want errValidateBodyBudget while the first is held", err)
	}
	// A body of unknown length is reserved chunk by chunk: the chunk that
	// would cross the budget is refused, and nothing stays reserved.
	if _, _, err := b.read(bytes.NewReader(first), -1); !errors.Is(err, errValidateBodyBudget) {
		t.Fatalf("unknown-length body = %v, want errValidateBodyBudget", err)
	}
	if b.used != held {
		t.Fatalf("used = %d after refusals, want only the first body's %d", b.used, held)
	}
	b.release(held)
	small := []byte(`{"ok":true}`)
	got, held, err = b.read(bytes.NewReader(small), -1)
	if err != nil || !bytes.Equal(got, small) || held != int64(len(small)) {
		t.Fatalf("small unknown-length read = %q, held %d, err %v; want the body and only its bytes held", got, held, err)
	}
	b.release(held)
	if b.used != 0 {
		t.Fatalf("used = %d after every release, want 0", b.used)
	}
	var unbounded *validateBodyBudget
	if got, held, err := unbounded.read(bytes.NewReader(first), int64(len(first))); err != nil || held != 0 || len(got) != len(first) {
		t.Fatalf("nil budget read = %d bytes, held %d, err %v", len(got), held, err)
	}
}
