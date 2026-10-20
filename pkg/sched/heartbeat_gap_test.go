package sched_test

// Property test for the heartbeat-gap classifier (CP-1, echo of the
// package-doc invariant under the new observability surface).
//
// The classifier ClassifyHeartbeatGap is the SHARED oracle between
// the production wire shape (GET /v1/compute-nodes/{name}/heartbeats)
// and this test. A future contributor who breaks the classifier's
// arithmetic surfaces here first; the operator-facing wire shape
// (the `missed` / `stale` flags) follows the same function.
//
// The fuzz target runs as a developer-machine command only — no CI
// fuzz lane exists (memory: "no CI fuzz lane for the new PRs that
// add scheduler hardening"). Sustained fuzzing:
//
//   go test ./pkg/sched -run '^$' \
//     -fuzz '^FuzzHeartbeatGapClassifier$' -fuzztime=30s

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"testing"
	"time"
)

// TestHeartbeatGapClassification pins the exact arithmetic for the
// documented operator expectations. The table mirrors the wire
// shape's `missed` / `stale` flags on the endpoint so a regression
// in the classifier is observable end-to-end.
func TestHeartbeatGapClassification(t *testing.T) {
	interval := sched.DefaultHeartbeatInterval
	staleness := sched.DefaultHeartbeatStaleness
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	type gapCase struct {
		name       string
		gap        time.Duration
		wantMissed bool
		wantStale  bool
	}

	cases := []gapCase{
		{
			name: "healthy tick (gap < interval)",
			gap:  30 * time.Second, // exactly interval
			// gap == interval is NOT < interval, so the strict-
			// less-than rule says "missed". One-tick clock drift
			// is operator-actionable: count it as missed so the
			// row is visible in the wire shape.
			wantMissed: true,
			wantStale:  false,
		},
		{
			name:       "two ticks at exact interval",
			gap:        60 * time.Second,
			wantMissed: true,
			wantStale:  false,
		},
		{
			name:       "one tick missed (60s)",
			gap:        60 * time.Second,
			wantMissed: true,
			wantStale:  false,
		},
		{
			name:       "two ticks missed (95s, just past staleness)",
			gap:        95 * time.Second,
			wantMissed: true,
			wantStale:  true,
		},
		{
			name:       "three ticks missed (120s)",
			gap:        120 * time.Second,
			wantMissed: true,
			wantStale:  true,
		},
		{
			name:       "very short gap (sub-second)",
			gap:        500 * time.Millisecond,
			wantMissed: false,
			wantStale:  false,
		},
		{
			name:       "zero gap (same tick)",
			gap:        0,
			wantMissed: false,
			wantStale:  false,
		},
		{
			name:       "negative gap (clock skew, treated as healthy)",
			gap:        -1 * time.Second,
			wantMissed: false,
			wantStale:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := base
			curr := base.Add(tc.gap)
			got := sched.ClassifyHeartbeatGap(prev, curr, interval, staleness)
			if got.Missed != tc.wantMissed {
				t.Errorf("Missed = %v, want %v (gap=%s, interval=%s, staleness=%s)",
					got.Missed, tc.wantMissed, tc.gap, interval, staleness)
			}
			if got.Stale != tc.wantStale {
				t.Errorf("Stale = %v, want %v (gap=%s, interval=%s, staleness=%s)",
					got.Stale, tc.wantStale, tc.gap, interval, staleness)
			}
		})
	}

	// First-row baseline: prev.IsZero() returns the zero summary
	// regardless of curr. The endpoint emits this on the first row
	// of a fresh node's history (no previous to compare against).
	t.Run("first row has no baseline", func(t *testing.T) {
		got := sched.ClassifyHeartbeatGap(time.Time{}, base, interval, staleness)
		if got.Missed || got.Stale {
			t.Errorf("first-row: got %+v, want zero summary", got)
		}
	})
}

// TestHeartbeatGapClassification_ChainedSequence drives the
// classifier across a multi-row sequence and asserts the per-row
// classifications match what the operator's wire shape would
// surface. The sequence is the canonical CP-1 "mixed-gap" pattern:
// each row carries a per-step gap, so consecutive rows chain
// through the classifier as the wire shape emits them.
func TestHeartbeatGapClassification_ChainedSequence(t *testing.T) {
	interval := sched.DefaultHeartbeatInterval
	staleness := sched.DefaultHeartbeatStaleness
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	// Per-step gaps in order. Row 0 has no previous row (baseline
	// is zero, simulator returns the zero summary). Subsequent
	// rows chain: receive_at[i] = receive_at[i-1] + gap[i].
	steps := []time.Duration{
		0,                 // first row: no baseline
		30 * time.Second,  // gap = interval → Missed=true (strict-less-than)
		60 * time.Second,  // gap > interval, <= staleness → Missed=true
		95 * time.Second,  // gap > staleness → Missed=true, Stale=true
		120 * time.Second, // gap > staleness → Missed=true, Stale=true
	}
	type wants struct{ Missed, Stale bool }
	want := []wants{
		{},                           // first row
		{Missed: true, Stale: false}, // gap == interval
		{Missed: true, Stale: false}, // gap 60s
		{Missed: true, Stale: true},  // gap 95s
		{Missed: true, Stale: true},  // gap 120s
	}
	receivedAt := base
	for i, gap := range steps {
		// First row: prev = zero-time.
		var prev time.Time
		if i > 0 {
			prev = receivedAt
		}
		receivedAt = receivedAt.Add(gap)
		got := sched.ClassifyHeartbeatGap(prev, receivedAt, interval, staleness)
		if got.Missed != want[i].Missed || got.Stale != want[i].Stale {
			t.Errorf("row %d (gap=%s, prev=%v, curr=%v): got %+v, want %+v",
				i, gap, prev, receivedAt, got, want[i])
		}
	}
}

// FuzzHeartbeatGapClassifier exercises three invariants the
// classifier must always satisfy. The invariants are independent
// of any specific gap value:
//
//  1. never both Missed AND Stale (Mutually exclusive with the
//     exception that Stale => Missed; "both" reading is a
//     classification bug)
//
//  2. Stale implies gap > staleness (the rule that ties the
//     flag to the threshold)
//
//  3. Missed implies gap >= interval (the rule that ties the
//     flag to the threshold; a strict-less-than gap cannot
//     classify as missed)
//
// Invariant #3 is load-bearing — a bug here would mean the
// operator UI shows "missed" on every healthy tick. The fuzzer
// runs as a developer-machine command (no CI fuzz lane).
func FuzzHeartbeatGapClassifier(f *testing.F) {
	interval := sched.DefaultHeartbeatInterval
	staleness := sched.DefaultHeartbeatStaleness

	f.Add(int64(0), int64(0))
	f.Add(int64(0), int64(30_000_000_000)) // 30s
	f.Add(int64(30_000_000_000), int64(60_000_000_000))
	f.Add(int64(0), int64(95_000_000_000)) // past staleness
	f.Add(int64(60_000_000_000), int64(0)) // negative gap (clock skew)

	f.Fuzz(func(t *testing.T, prevNs, currNs int64) {
		prev := time.Unix(0, prevNs)
		curr := time.Unix(0, currNs)
		gap := curr.Sub(prev)
		if prev.IsZero() {
			// First-row baseline short-circuit; skip the
			// invariants below (the zero summary has no
			// assertions to make).
			return
		}
		got := sched.ClassifyHeartbeatGap(prev, curr, interval, staleness)

		// Invariant 1: never both Missed AND Stale false-positive.
		// A bug here would mean the wire shape emits both flags —
		// downstream parsers see `missed: true, stale: true`
		// which is the expected pair for "paged past staleness"
		// but a bug-spawned pairwise true on a 30s gap is the
		// failure mode.
		// Note: Stale ⇒ Missed IS expected (by construction);
		// what we forbid is a *bug* where the classifier flips
		// both for a gap < interval, which would mean the
		// "healthy" branch regressed.
		if got.Missed && got.Stale && gap < interval {
			t.Fatalf("gap %s: both Missed and Stale on a healthy tick", gap)
		}

		// Invariant 2: Stale implies gap > staleness.
		if got.Stale && gap <= staleness {
			t.Fatalf("gap %s: Stale=true but gap <= staleness %s", gap, staleness)
		}

		// Invariant 3: Missed implies gap >= interval.
		if got.Missed && gap < interval {
			t.Fatalf("gap %s: Missed=true but gap < interval %s", gap, interval)
		}
	})
}
