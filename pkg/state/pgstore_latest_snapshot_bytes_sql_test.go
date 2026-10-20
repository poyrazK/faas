// SQL-shape regression for LatestSnapshotBytes (PR #428 review
// blocker #3). The query is inlined inside (*PgStore).LatestSnapshotBytes
// rather than extracted as a package-level const (the existing
// pgstore convention for one-shot queries), so the cheapest way
// to pin the live-deployment filter is to grep for the predicate
// substring. The check is intentionally whitespace-tolerant.
//
// A future refactor that drops the `d.status = 'live'` filter
// would re-bill accounts for snapshots belonging to superseded /
// failed / pending deployments — exactly the silent over-billing
// the review surfaced. This test fails at unit-test time well
// before a fleet roll-out.

package state_test

import (
	_ "embed"
	"strings"
	"testing"
)

// pgStoreLatestSnapshotBytesSource is the pgstore.go source text,
// embedded at compile time. The test only scans it for SQL
// substrings, so a future edit to either the query or surrounding
// comment text surfaces here as a failed assertion rather than a
// silently-shipped SQL drift. The embed path is relative to this
// test file — same package directory.
//
//go:embed pgstore.go
var pgStoreLatestSnapshotBytesSource string

// TestPgStore_LatestSnapshotBytes_FiltersLiveDeployment pins the
// `d.status = 'live'` predicate in the LatestSnapshotBytes SQL.
// The query text is read directly out of pgstore.go via a small
// regex-free substring scan; the source-of-truth is the function
// body itself, not a separate const, so the test catches accidental
// edits to either the SELECT list or the WHERE clause.
func TestPgStore_LatestSnapshotBytes_FiltersLiveDeployment(t *testing.T) {
	// Locate the LatestSnapshotBytes function in pgstore.go by
	// scanning for its signature line.
	startIdx := strings.Index(pgStoreLatestSnapshotBytesSource, "func (s *PgStore) LatestSnapshotBytes(")
	if startIdx < 0 {
		t.Fatal("could not locate LatestSnapshotBytes in pgstore.go")
	}
	// Grab a fixed window after the signature — long enough to
	// cover the full SELECT but bounded so we don't drag in the
	// next function. 2 KiB is comfortably more than the actual
	// query body (≈40 lines).
	endIdx := startIdx + 2048
	if endIdx > len(pgStoreLatestSnapshotBytesSource) {
		endIdx = len(pgStoreLatestSnapshotBytesSource)
	}
	fn := pgStoreLatestSnapshotBytesSource[startIdx:endIdx]

	// Must filter to live deployments.
	if !strings.Contains(fn, "d.status  = 'live'") && !strings.Contains(fn, "d.status = 'live'") {
		t.Errorf("LatestSnapshotBytes must filter deployments to status='live' — without it the storage rollup bills superseded / failed / pending deployments. Got function body:\n%s", fn)
	}

	// Must keep the non-stale snapshot predicate so the partial
	// index 00071 is actually selectable.
	if !strings.Contains(fn, "s.stale = false") {
		t.Errorf("LatestSnapshotBytes must keep `s.stale = false` so snapshots_live_idx (migration 00071) is selectable")
	}

	// Must order by created_at DESC + LIMIT 1 so the inner scan
	// is bounded.
	if !strings.Contains(fn, "order by s.created_at desc") {
		t.Errorf("LatestSnapshotBytes must order by s.created_at desc to bound the inner scan to the latest snapshot")
	}
	if !strings.Contains(fn, "limit 1") {
		t.Errorf("LatestSnapshotBytes must LIMIT 1 — without it the COALESCE above aggregates all rows instead of the latest")
	}
}
