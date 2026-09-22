// ListEventsBySidecar (issue #463 / ADR-069 / PR-B) in-memory
// twin tests. Mirrors the memstore_wake_id_test.go shape so the
// pgstore and MemStore implementations stay in lockstep on:
// (1) closed-kind filter (only the three wake.sidecar_* lifecycle
//     kinds return),
// (2) sidecar_name payload filter (matching key wins),
// (3) at-order ASC (insertion order is NOT at-order — the in-memory
//     append path runs under different locks).
// The pgstore impl uses the same shape with raw SQL; the test
// surface is the MemStore twin's seam.

package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestMemStore_ListEventsBySidecar_FiltersByKindAndName pins the
// load-bearing filter behaviour: a wake.sidecar_init_exit event
// for "metrics" is returned, the same event for "logger" is not,
// and a non-sidecar event (e.g. wake.boot_started) with
// sidecar_name in the payload is filtered out by the closed
// kind enum. A future event that reuses the sidecar_name key
// would never bleed into a sidecar's audit view.
func TestMemStore_ListEventsBySidecar_FiltersByKindAndName(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	now := time.Now()
	if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_init_exit", nil,
		mustJSON(t, map[string]any{"sidecar_name": "metrics", "status": "init_ok", "exit_code": 0})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_init_exit", nil,
		mustJSON(t, map[string]any{"sidecar_name": "logger", "status": "init_ok", "exit_code": 0})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_restart", nil,
		mustJSON(t, map[string]any{"sidecar_name": "metrics", "attempt": 1, "previous_exit_code": 1})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_health", nil,
		mustJSON(t, map[string]any{"sidecar_name": "metrics", "status": "healthy"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := m.AppendEvent(ctx, "vmmd", "wake.boot_started", nil,
		mustJSON(t, map[string]any{"sidecar_name": "metrics", "instance_id": "i-1"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = now
	got, err := m.ListEventsBySidecar(ctx, "metrics", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsBySidecar: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3 (init_ok metrics + restart metrics + health, boot_started filtered)", len(got))
	}
	if got[0].Kind != "wake.sidecar_init_exit" || got[1].Kind != "wake.sidecar_restart" || got[2].Kind != "wake.sidecar_health" {
		t.Errorf("order = [%s, %s, %s], want [init_exit, restart, health]", got[0].Kind, got[1].Kind, got[2].Kind)
	}
}

// TestMemStore_ListEventsBySidecar_Limit pins the limit cap
// (mirrors ListEventsByWakeID's behaviour). limit=0 means no
// cap; limit>0 truncates the at-ordered result.
func TestMemStore_ListEventsBySidecar_Limit(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	for i := 0; i < 5; i++ {
		if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_init_exit", nil,
			mustJSON(t, map[string]any{"sidecar_name": "metrics", "status": "init_ok"})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got, err := m.ListEventsBySidecar(ctx, "metrics", time.Time{}, 2)
	if err != nil {
		t.Fatalf("ListEventsBySidecar: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2 (limit=2)", len(got))
	}
}

// TestMemStore_ListEventsBySidecar_NoMatch pins the empty-result
// path: a sidecar name with no events returns an empty slice
// (NOT an error). Caller-side code paths must treat the empty
// result as "no audit rows" not "query failed".
func TestMemStore_ListEventsBySidecar_NoMatch(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	if err := m.AppendEvent(ctx, "vmmd", "wake.sidecar_init_exit", nil,
		mustJSON(t, map[string]any{"sidecar_name": "logger"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := m.ListEventsBySidecar(ctx, "metrics", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsBySidecar: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows, want 0", len(got))
	}
}

// mustJSON is a tiny marshalling helper used by this test file;
// errors fail the test rather than panic so a missing field
// surfaces as a clear "append: ..." failure instead of a
// goroutine-killing panic inside the per-row path.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
