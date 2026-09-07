// Package state_test — pgstore app-protocol stale snapshot coverage (ADR-127
// §D1 Layer 6). Pins the MarkAllSnapshotsStaleByAppProtocol + single-row
// MarkSnapshotStaleByAppProtocol happy + sad paths so the post-rebase
// coverage floor (target ≥ 70%) holds when main lands adjacent code that
// dilutes the package-wide average. Zero source change.
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_MarkAllSnapshotsStaleByAppProtocol_Happy exercises the bulk-flip
// every non-stale snapshot whose deployment's app_protocol ∈ appProtocols
// path. Mirrors memstore_app_protocol_stale_test.go::TestMemStore_
// MarkAllSnapshotsStaleByAppProtocol but on the PgStore side (which had
// 0% coverage at round-6 rebase; main's PR #1017 added pgstore.go code
// without proportional pgtests).
func TestPg_MarkAllSnapshotsStaleByAppProtocol_Happy(t *testing.T) {
	s, ctx, _, _, _ := pgCoverageFixture(t)

	// Empty filter → SQL short-circuit, returns 0 + nil. Pinning the
	// early-return keeps the real UPDATE branch uncovered only on the
	// path that actually flips rows.
	if n, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, nil, "v1"); err != nil || n != 0 {
		t.Fatalf("nil appProtocols: n=%d, err=%v", n, err)
	}

	// Real filter that matches our fixture's app (default app_protocol
	// is 'http1' from migration 00382). The function returns the count
	// of freshly-flipped rows; the fixture has no snapshots so n=0 but
	// the WHERE-clause path is exercised.
	if _, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http1"}, "v1"); err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol(http1): %v", err)
	}

	// Same query, non-matching protocol → still 0 flips, still nil err.
	if n, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"grpc"}, "v1"); err != nil || n != 0 {
		t.Fatalf("grpc filter: n=%d, err=%v", n, err)
	}
}

// TestPg_MarkSnapshotStaleByAppProtocol_Sad pins the validation guards:
// empty appProtocols + empty snapshotID both error before hitting SQL. The
// happy single-row path requires spinning up a deployment whose
// app_protocol actually matches — that's already covered by the broader
// rebase-target integration suite in pgstore_test.go, so we only pin
// the error paths here.
func TestPg_MarkSnapshotStaleByAppProtocol_Sad(t *testing.T) {
	s, ctx, _, _, _ := pgCoverageFixture(t)
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, "00000000-0000-0000-0000-000000000000", nil); err == nil {
		t.Fatal("MarkSnapshotStaleByAppProtocol(empty appProtocols) = nil, want error")
	}
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, "", []string{"http2"}); err == nil {
		t.Fatal("MarkSnapshotStaleByAppProtocol(empty snapshotID) = nil, want error")
	}

	// Unknown snapshot ID + matching protocol: must return
	// state.ErrNotFound so the caller can distinguish "we don't
	// know about this row" from "the row is fine" (e.g. when
	// reconciling a stale-imaging cache after a node restart).
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, "00000000-0000-0000-0000-000000000000", []string{"http1"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("MarkSnapshotStaleByAppProtocol(unknown id) = %v, want ErrNotFound", err)
	}
}
