package requestjournal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func openTestStore(t *testing.T, dir string, c *clock, budget int64, onEvict func(int64)) *Store {
	t.Helper()
	store, err := Open(dir, Config{Span: 10 * time.Minute, Grace: time.Minute, Budget: budget, Now: c.Now, OnEvict: onEvict})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func record(account, app uuid.UUID, requestID string, received time.Time) Record {
	return Record{AccountID: account, AppID: app, RequestID: requestID, TraceID: "trace-" + requestID,
		ReceivedAt: received, ExpiresAt: received.Add(3 * 24 * time.Hour)}
}

func mustLookup(t *testing.T, store *Store, account, app uuid.UUID, requestID string, from, until time.Time) (Record, bool) {
	t.Helper()
	got, ok, err := store.Lookup(account, app, requestID, from, until)
	if err != nil {
		t.Fatalf("Lookup(%s): %v", requestID, err)
	}
	return got, ok
}

func TestStoreFindsRecordsInOpenClosedAndReopenedSegments(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c := &clock{now: start.Add(time.Minute)}
	store := openTestStore(t, dir, c, 1<<30, nil)
	account, app, other := uuid.New(), uuid.New(), uuid.New()
	var batch []Record
	for i := range 500 {
		batch = append(batch, record(account, app, uuid.NewString(), start.Add(time.Duration(i)*time.Second)))
	}
	batch = append(batch, record(account, other, "shared-id", start.Add(time.Second)))
	if skipped, err := store.Append(batch); err != nil || skipped != 0 {
		t.Fatalf("Append: skipped=%d err=%v", skipped, err)
	}
	from, until := start.Add(-time.Hour), start.Add(time.Hour)
	want := batch[123]
	check := func(stage string) {
		t.Helper()
		got, ok := mustLookup(t, store, account, app, want.RequestID, from, until)
		if !ok || got.RequestID != want.RequestID || got.TraceID != want.TraceID || !got.ReceivedAt.Equal(want.ReceivedAt) {
			t.Fatalf("%s: lookup = %+v, %v; want %+v", stage, got, ok, want)
		}
		if _, ok := mustLookup(t, store, account, app, "shared-id", from, until); ok {
			t.Fatalf("%s: a request ID of another app resolved for this app", stage)
		}
		if _, ok := mustLookup(t, store, uuid.New(), app, want.RequestID, from, until); ok {
			t.Fatalf("%s: another account resolved this app's request ID", stage)
		}
	}
	check("open segment")

	c.now = start.Add(30 * time.Minute) // both segments are past span and grace
	if err := store.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "1793534400.idx")); err != nil {
		t.Fatalf("closing did not write the first segment's index: %v", err)
	}
	check("closed segment")

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	store = openTestStore(t, dir, c, 1<<30, nil)
	check("reopened store")
}

func TestStoreRebuildsSegmentACrashLeftWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c := &clock{now: start.Add(time.Minute)}
	store := openTestStore(t, dir, c, 1<<30, nil)
	account, app := uuid.New(), uuid.New()
	first := record(account, app, "kept", start.Add(time.Second))
	if _, err := store.Append([]Record{first}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// A crash: no Close, so no index, and a frame torn halfway.
	seg := filepath.Join(dir, "1793534400.seg")
	f, err := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 40, 0, 0, 0, 1, 1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, _ := os.Stat(seg)

	store = openTestStore(t, dir, c, 1<<30, nil)
	after, _ := os.Stat(seg)
	if after.Size() >= before.Size() {
		t.Fatalf("torn frame was not cut: %d -> %d bytes", before.Size(), after.Size())
	}
	if got, ok := mustLookup(t, store, account, app, "kept", start.Add(-time.Hour), start.Add(time.Hour)); !ok || got.RequestID != "kept" {
		t.Fatalf("record before the torn frame was lost: %+v, %v", got, ok)
	}
	if _, err := store.Append([]Record{record(account, app, "after", start.Add(2*time.Second))}); err != nil {
		t.Fatalf("Append after recovery: %v", err)
	}
	if _, ok := mustLookup(t, store, account, app, "after", start.Add(-time.Hour), start.Add(time.Hour)); !ok {
		t.Fatal("record appended after recovery did not resolve")
	}
}

func TestStoreRoutesLateRecordsToTheOpenSegment(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c := &clock{now: start.Add(25 * time.Minute)} // the 12:00 segment is closed
	store := openTestStore(t, dir, c, 1<<30, nil)
	account, app := uuid.New(), uuid.New()
	late := record(account, app, "late", start.Add(5*time.Minute))
	if _, err := store.Append([]Record{late}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "1793534400.seg")); !os.IsNotExist(err) {
		t.Fatalf("late record reopened a closed segment: %v", err)
	}
	if _, ok := mustLookup(t, store, account, app, "late", start, start.Add(10*time.Minute)); !ok {
		t.Fatal("late record outside its own segment did not resolve within its received window")
	}
}

func TestStoreExpiresAndEvicts(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c := &clock{now: start.Add(time.Minute)}
	var evicted int64
	store := openTestStore(t, dir, c, 1<<30, func(n int64) { evicted += n })
	account, app := uuid.New(), uuid.New()
	short := record(account, app, "short", start.Add(time.Second))
	short.ExpiresAt = start.Add(time.Hour)
	if _, err := store.Append([]Record{short}); err != nil {
		t.Fatal(err)
	}
	c.now = start.Add(2 * time.Hour)
	if _, ok := mustLookup(t, store, account, app, "short", start.Add(-time.Hour), start.Add(time.Hour)); ok {
		t.Fatal("an expired record resolved before the sweep removed it")
	}
	if err := store.Sweep(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("expired segment left %d files", len(entries))
	}

	// Budget: two closed segments and a budget that fits one.
	for i, minute := range []int{0, 10} {
		at := c.now.Truncate(10 * time.Minute).Add(time.Duration(minute) * time.Minute)
		c.now = at.Add(time.Second)
		if _, err := store.Append([]Record{record(account, app, "budget"+string(rune('a'+i)), at)}); err != nil {
			t.Fatal(err)
		}
	}
	c.now = c.now.Add(30 * time.Minute)
	if err := store.Sweep(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var size int64
	for _, entry := range entries {
		info, _ := entry.Info()
		size += info.Size()
	}
	store.cfg.Budget = size/2 + 1
	if err := store.Sweep(); err != nil {
		t.Fatal(err)
	}
	if evicted != 1 {
		t.Fatalf("evicted %d records, want the oldest segment's 1", evicted)
	}
	if _, ok := mustLookup(t, store, account, app, "budgetb", c.now.Add(-2*time.Hour), c.now); !ok {
		t.Fatal("the newest segment was evicted instead of the oldest")
	}
}

func TestStoreSkipsInvalidRecords(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 1, 12, 1, 0, 0, time.UTC)}
	store := openTestStore(t, t.TempDir(), c, 1<<30, nil)
	long := make([]byte, maxFieldBytes+1)
	for i := range long {
		long[i] = 'x'
	}
	skipped, err := store.Append([]Record{
		record(uuid.New(), uuid.New(), "", c.now),
		record(uuid.New(), uuid.New(), string(long), c.now),
		record(uuid.New(), uuid.New(), "ok", c.now),
	})
	if err != nil || skipped != 2 {
		t.Fatalf("Append: skipped=%d err=%v, want 2 skipped", skipped, err)
	}
}
