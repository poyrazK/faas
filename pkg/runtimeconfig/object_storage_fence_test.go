package runtimeconfig

import (
	"context"
	"errors"
	"testing"
)

type fakeTimelines struct {
	accepted, current string
	err               error
}

func (f *fakeTimelines) ObjectStorageTimelines(context.Context) (string, string, error) {
	return f.accepted, f.current, f.err
}

// adr: 20261025111015 — object storage serves only on the accepted
// PostgreSQL timeline: closed before the first read, closed after a restore
// promoted onto a new one, open again once that one is accepted, and a
// failed read keeps the last answer.
func TestObjectStorageFence(t *testing.T) {
	ctx := context.Background()
	store := &fakeTimelines{accepted: "00000001", current: "00000001"}
	fence := NewObjectStorageFence(store, nil)
	if fence.Open() {
		t.Fatal("open before the first read")
	}
	check := func(want bool) {
		t.Helper()
		if err := fence.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if fence.Open() != want {
			t.Fatalf("open = %v with accepted %q current %q, want %v", fence.Open(), store.accepted, store.current, want)
		}
	}
	check(true)
	store.current = "00000002" // a restore promoted
	check(false)
	store.err = errors.New("database down")
	if err := fence.Check(ctx); err == nil || fence.Open() {
		t.Fatalf("a failed read = %v, open %v; want the error and the fence kept closed", err, fence.Open())
	}
	store.err, store.accepted = nil, "00000002"
	check(true)
	store.accepted = "" // no acceptance yet: a release before the fence
	check(true)
	var unset *ObjectStorageFence
	if unset.Open() {
		t.Fatal("a nil fence is open")
	}
}
