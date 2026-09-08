package state_test

// MemStore parity coverage for ADR-127 §D1 (Layer 6) — the
// app-protocol stale-mark methods on Store. Mirrors
// memstore_test.go::TestMemStore_MarkAllSnapshotsStaleByFCVersion
// so the F2/F3 sweep behaviour stays 1:1 across the in-memory
// and PG paths.
//
// The PG path is exercised by the same shape in
// pgstore_warm_snapshot_test.go (//go:build !no_pg) — see the
// commit-3 mirror at pkg/state/pgstore.go:9802 for the SQL
// UPDATE shape.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestMemStore_MarkAllSnapshotsStaleByAppProtocol covers the bulk
// F3 sweep: every non-stale snapshot whose deployment's
// app.app_protocol ∈ {http2, grpc} is flipped stale; http1 is
// untouched; empty input is no-op; idempotent second call.
func TestMemStore_MarkAllSnapshotsStaleByAppProtocol(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()

	acct, err := m.CreateAccount(ctx, "aprot-f3@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Three apps: one on each protocol. Snapshots for the http1 app
	// must NEVER be flipped.
	insertApp := func(slug, protocol string) string {
		a, err := m.CreateApp(ctx, state.App{
			AccountID:      acct.ID,
			Slug:           slug,
			RAMMB:          256,
			IdleTimeoutS:   30,
			MaxConcurrency: 2,
			AppProtocol:    protocol,
		})
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", slug, err)
		}
		return a.ID
	}
	appHTTP1 := insertApp("aprot-http1", "http1")
	appHTTP2 := insertApp("aprot-http2", "http2")
	appGRPC := insertApp("aprot-grpc", "grpc")
	appHTTP2Current := insertApp("aprot-http2-current", "http2")

	// Deployment + snapshot factory per app.
	insertSnap := func(appID, label, baseImageVersion string) string {
		dep, err := m.CreateDeployment(ctx, state.Deployment{
			AppID:       appID,
			Kind:        state.DeploymentKindImage,
			ImageDigest: "sha256:" + label,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%s): %v", label, err)
		}
		// CreateSnapshot signature: includes the new Tier field
		// post-issue #470; defaulting to SnapshotTierInit is fine
		// here since F3 sweeps all tiers.
		snap, err := m.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID:     dep.ID,
			MemBytes:         100,
			DiskBytes:        100,
			FCVersion:        "1.13.0",
			BaseImageVersion: baseImageVersion,
			StorageKey:       state.SnapMemKey(dep.ID),
			Tier:             state.SnapshotTierInit,
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", label, err)
		}
		return snap.ID
	}
	http1ID := insertSnap(appHTTP1, "h1", "v0")
	http2ID := insertSnap(appHTTP2, "h2", "v0")
	grpcID := insertSnap(appGRPC, "g", "v0")
	http2CurrentID := insertSnap(appHTTP2Current, "h2-current", "v1")

	// Bulk sweep — the F3 close-set is {http2, grpc}.
	n, err := m.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http2", "grpc"}, "v1")
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol: %v", err)
	}
	if n != 2 {
		t.Errorf("marked %d stale, want 2 (http2 + grpc rows)", n)
	}

	// Verify via the public API path: ListSnapshotsForGC joins
	// apps and projects Stale + Slug; check each row by id.
	verify := func(id string, wantStale bool, label string) {
		for _, row := range mustSnapshotsForGC(t, m, ctx) {
			if row.ID == id && row.Stale != wantStale {
				t.Errorf("%s snapshot Stale=%v, want %v", label, row.Stale, wantStale)
			}
		}
	}
	verify(http1ID, false, "http1")
	verify(http2ID, true, "http2")
	verify(grpcID, true, "grpc")
	verify(http2CurrentID, false, "http2 current base")

	// Idempotency — second call finds zero non-stale rows.
	n2, err := m.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http2", "grpc"}, "v1")
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("idempotent second sweep flipped %d, want 0", n2)
	}

	// Empty filter is a no-op (matches the SQL behaviour).
	n3, _ := m.MarkAllSnapshotsStaleByAppProtocol(ctx, nil, "v1")
	if n3 != 0 {
		t.Errorf("empty filter flipped %d, want 0", n3)
	}
	if _, err := m.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http2"}, ""); err == nil {
		t.Error("empty current base image version must fail")
	}
}

// TestMemStore_MarkSnapshotStaleByAppProtocol covers the single-row
// mirror: id + protocol-set match → flipped; id matches but
// protocol is http1 → ErrNotFound; id doesn't exist → ErrNotFound;
// empty inputs are caller bugs.
func TestMemStore_MarkSnapshotStaleByAppProtocol(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()

	acct, _ := m.CreateAccount(ctx, "aprot-single@example.com", "hobby")
	http1App, _ := m.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "aprot-single-h1",
		RAMMB:          128,
		IdleTimeoutS:   30,
		MaxConcurrency: 1,
		AppProtocol:    "http1",
	})
	http2App, _ := m.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "aprot-single-h2",
		RAMMB:          256,
		IdleTimeoutS:   30,
		MaxConcurrency: 2,
		AppProtocol:    "http2",
	})

	mkSnap := func(appID string) string {
		dep, _ := m.CreateDeployment(ctx, state.Deployment{
			AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:single",
		})
		snap, _ := m.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion: "1.13.0", StorageKey: state.SnapMemKey(dep.ID),
			Tier: state.SnapshotTierInit,
		})
		return snap.ID
	}
	http1Snap := mkSnap(http1App.ID)
	http2Snap := mkSnap(http2App.ID)

	// 1. id exists + protocol matches → flipped, no error.
	if err := m.MarkSnapshotStaleByAppProtocol(ctx, http2Snap, []string{"http2", "grpc"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}

	// 2. id exists + protocol does NOT match (http1 row, sweep = {http2,grpc})
	// → ErrNotFound (caller distinguishes "row exists but wrong protocol" from
	// "row doesn't exist at all").
	err := m.MarkSnapshotStaleByAppProtocol(ctx, http1Snap, []string{"http2", "grpc"})
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound for http1 row in http2/grpc sweep, got %v", err)
	}

	// 3. id doesn't exist → ErrNotFound.
	err = m.MarkSnapshotStaleByAppProtocol(ctx, "00000000-0000-0000-0000-000000000000", []string{"http2"})
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing id, got %v", err)
	}

	// 4. Empty appProtocols is a caller bug.
	if err := m.MarkSnapshotStaleByAppProtocol(ctx, http1Snap, nil); err == nil {
		t.Errorf("expected error for empty appProtocols, got nil")
	}
	if err := m.MarkSnapshotStaleByAppProtocol(ctx, "", []string{"http2"}); err == nil {
		t.Errorf("expected error for empty snapshotID, got nil")
	}
}

// mustSnapshotsForGC is the public-API projection: returns the
// join snapshot rows so the test can assert on the Stale column
// without touching unexported fields.
func mustSnapshotsForGC(t *testing.T, m *state.MemStore, ctx context.Context) []state.SnapshotForGC {
	t.Helper()
	rows, err := m.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	return rows
}
