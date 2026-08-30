// snapshot_backoff_test.go — coverage for the capped-
// exponential backoff math + the store-stamp wrappers
// (Workstream B / issue #1184 / ADR-137).
package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestComputeSnapshotBackoff_Table pins the closed sequence
// from the spec: 5s → 10s → 20s → 40s → 80s → 160s → 300s
// (frozen at max). The pure-function form means a future
// change to the curve lands here as a one-line update; the
// dashboard's Retry-After header relies on the table.
func TestComputeSnapshotBackoff_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		misses            int
		wantDur           time.Duration
		wantRetryAfterSec int
	}{
		{0, 5 * time.Second, 5},
		{1, 10 * time.Second, 10},
		{2, 20 * time.Second, 20},
		{3, 40 * time.Second, 40},
		{4, 80 * time.Second, 80},
		{5, 160 * time.Second, 160},
		{6, 300 * time.Second, 300},
		{7, 300 * time.Second, 300}, // frozen at max
		{100, 300 * time.Second, 300},
		{-1, 5 * time.Second, 5}, // negative input clamps to 0
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			t.Parallel()
			d, ra := ComputeSnapshotBackoff(tc.misses)
			if d != tc.wantDur {
				t.Errorf("duration = %s, want %s", d, tc.wantDur)
			}
			if ra != tc.wantRetryAfterSec {
				t.Errorf("retryAfter = %d, want %d", ra, tc.wantRetryAfterSec)
			}
		})
	}
}

// TestRecordSnapshotMiss_StampsBackoff — the wrapper invokes
// ComputeSnapshotBackoff and stamps the row via the store.
// state.MemStore's DeploymentRecordSnapshotMiss is a no-op
// (the schema is read-only in test); the test verifies the
// wrapper returns the curve duration + retry-after seconds
// and tolerates the no-op stamp.
func TestRecordSnapshotMiss_StampsBackoff(t *testing.T) {
	e := &Engine{store: &state.MemStore{}}
	d, ra, err := e.RecordSnapshotMiss(t.Context(), "dep-1", 3)
	if err != nil {
		t.Errorf("RecordSnapshotMiss = %v, want nil", err)
	}
	if d != 40*time.Second {
		t.Errorf("duration = %s, want 40s", d)
	}
	if ra != 40 {
		t.Errorf("retryAfter = %d, want 40", ra)
	}
}

// TestClearSnapshotBackoff_NoOpOnNilEngine — same nil-safety
// pattern as the recreate primitive: a unit-test engine
// without a wired store can call Clear without panicking.
func TestClearSnapshotBackoff_NoOpOnNilEngine(t *testing.T) {
	var e *Engine
	if err := e.ClearSnapshotBackoff(t.Context(), "dep-1"); err != nil {
		t.Errorf("ClearSnapshotBackoff(nil) = %v, want nil", err)
	}
}
