// memstore_sidecar_layers_test.go — in-process round-trip tests for
// the per-workload filesystem handle (issue #463 / ADR-069 /
// PR-B). Mirrors pgstore_sidecar_layers_test.go so a regression in
// the in-memory store surfaces in unit tests without PG.
package state_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// memSidecarLayersFixture creates an account + app + deployment
// for in-process sidecar-layer tests.
func memSidecarLayersFixture(t *testing.T) (*state.MemStore, context.Context, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	m, _, app := memSidecarsFixture(t)
	dep, err := m.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:mem-sidecar-layer-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, dep
}

// TestMemStore_SetDeploymentSidecarLayer_UpsertRoundTrip mirrors
// the pgstore counterpart. The map-key-based upsert must match
// Postgres' PK semantics.
func TestMemStore_SetDeploymentSidecarLayer_UpsertRoundTrip(t *testing.T) {
	m, ctx, dep := memSidecarLayersFixture(t)

	first := state.DeploymentSidecarLayer{
		DeploymentID:  dep.ID,
		SidecarName:   "migrator",
		StorageKey:    "apps/m.ext4",
		Bytes:         1024,
		ContentDigest: "sha256:00",
	}
	got, err := m.SetDeploymentSidecarLayer(ctx, first)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if got.StorageKey != first.StorageKey {
		t.Errorf("first round-trip: got %+v", got)
	}

	second := state.DeploymentSidecarLayer{
		DeploymentID:  dep.ID,
		SidecarName:   "migrator",
		StorageKey:    "apps/m-v2.ext4",
		Bytes:         2048,
		ContentDigest: "sha256:01",
	}
	got2, err := m.SetDeploymentSidecarLayer(ctx, second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got2.StorageKey != second.StorageKey || got2.Bytes != 2048 {
		t.Errorf("second round-trip: got %+v", got2)
	}
	if !got2.UpdatedAt.After(got.UpdatedAt) && !got2.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: first=%v second=%v", got.UpdatedAt, got2.UpdatedAt)
	}
	if !got2.CreatedAt.Equal(got.CreatedAt) {
		t.Errorf("CreatedAt drifted: first=%v second=%v", got.CreatedAt, got2.CreatedAt)
	}
}

// TestMemStore_ListDeploymentSidecarLayers_Order verifies
// sidecar_name ASC ordering matches Postgres. Snapshot hashing
// relies on deterministic ordering.
func TestMemStore_ListDeploymentSidecarLayers_Order(t *testing.T) {
	m, ctx, dep := memSidecarLayersFixture(t)
	seed := []state.DeploymentSidecarLayer{
		{DeploymentID: dep.ID, SidecarName: "scraper", StorageKey: "s", Bytes: 1, ContentDigest: "sha256:00"},
		{DeploymentID: dep.ID, SidecarName: "migrator", StorageKey: "m", Bytes: 1, ContentDigest: "sha256:01"},
	}
	for _, l := range seed {
		if _, err := m.SetDeploymentSidecarLayer(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	out, err := m.ListDeploymentSidecarLayers(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d; want 2", len(out))
	}
	if out[0].SidecarName != "migrator" || out[1].SidecarName != "scraper" {
		t.Errorf("ordering: got [%q, %q]; want [migrator, scraper]",
			out[0].SidecarName, out[1].SidecarName)
	}
}

// TestMemStore_ListDeploymentSidecarLayers_Empty — empty slice,
// not nil, when no sidecars.
func TestMemStore_ListDeploymentSidecarLayers_Empty(t *testing.T) {
	m, ctx, dep := memSidecarLayersFixture(t)
	out, err := m.ListDeploymentSidecarLayers(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Errorf("got nil; want empty slice")
	}
	if len(out) != 0 {
		t.Errorf("got %d rows; want 0", len(out))
	}
}

// TestMemStore_SetDeploymentSidecarLayer_MissingDeployment —
// ErrNotFound when the FK target row doesn't exist (mirrors the
// pgstore defence-in-depth check).
func TestMemStore_SetDeploymentSidecarLayer_MissingDeployment(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	_, err := m.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID:  uuid.NewString(),
		SidecarName:   "orphan",
		StorageKey:    "x",
		Bytes:         1,
		ContentDigest: "sha256:00",
	})
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("got %v; want ErrNotFound", err)
	}
}

// Sanity guard — explicit interface compliance check so a
// future refactor on the Store interface can't silently drop
// the new methods without breaking compilation.
func TestMemStore_SatisfiesStoreInterface(t *testing.T) {
	var _ state.Store = (*state.MemStore)(nil)
	// The "must compile" property is sufficient; the assertion
	// here exists to point readers at the contract.
	_ = bytes.Runes // keep `bytes` import retained if other tests drop it
}
