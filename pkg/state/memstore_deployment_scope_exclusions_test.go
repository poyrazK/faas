//go:build !no_pg

// MemStore CRUD tests for the deployment_scope_exclusions surface
// (ADR-124 follow-up #3, migration 00418). Mirrors the
// memstore_app_webhooks_test.go shape — MemStore is the test
// fixture for handler-level tests, so the in-memory CRUD must
// match the Postgres invariants even though it has no SQL index
// to enforce them.
//
// Pins:
//  1. Create returns the row populated with id + timestamps.
//  2. Create rejects duplicate (account, project, slug) with
//     ErrConflict (the UNIQUE constraint).
//  3. List returns rows for the requested project sorted by
//     created_at DESC; rows past the 90-day window are filtered.
//  4. Delete is idempotent on the (account, project, slug)
//     composite key; returns ErrNotFound when no row matches.
//  5. Lookup returns rows for (account, project) sorted DESC; the
//     apply path folds these slugs into the per-deploy exclude
//     list.
package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// memstoreExclusionSeed is the canonical fixture for the
// DeploymentScopeExclusion CRUD tests. Keeping the shape in one
// place lets each sub-test assert a single contract.
func memstoreExclusionSeed(projectID string) state.DeploymentScopeExclusion {
	return state.DeploymentScopeExclusion{
		ID:        "mem-excl-1",
		AccountID: "acct-1",
		ProjectID: projectID,
		AppID:     "app-1",
		Slug:      "checkout-api",
		Reason:    "destructive in prod",
		CreatedBy: "operator@test",
	}
}

// TestMemStore_CreateDeploymentScopeExclusion pins the load-bearing
// insert contract: id + timestamps are stamped by the store.
func TestMemStore_CreateDeploymentScopeExclusion(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	in := memstoreExclusionSeed("proj-1")

	out, err := m.CreateDeploymentScopeExclusion(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.ID == "" {
		t.Errorf("create: id not stamped")
	}
	if out.CreatedAt.IsZero() {
		t.Errorf("create: created_at not stamped")
	}
	if out.UpdatedAt.IsZero() {
		t.Errorf("create: updated_at not stamped")
	}
	if out.Slug != in.Slug {
		t.Errorf("create: slug drift: got %q, want %q", out.Slug, in.Slug)
	}
}

// TestMemStore_CreateDeploymentScopeExclusion_DuplicateIsConflict
// pins the UNIQUE (account_id, project_id, slug) invariant. A
// second insert with the same triple must return ErrConflict so
// the handler returns 409 (mirroring PgStore's SQLSTATE 23505
// funnel).
func TestMemStore_CreateDeploymentScopeExclusion_DuplicateIsConflict(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	in := memstoreExclusionSeed("proj-1")

	if _, err := m.CreateDeploymentScopeExclusion(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := m.CreateDeploymentScopeExclusion(ctx, in)
	if !errors.Is(err, state.ErrConflict) {
		t.Errorf("duplicate create: got %v, want ErrConflict", err)
	}
}

// TestMemStore_ListDeploymentScopeExclusions_SortedAndScoped pins
// the list contract: results are project-scoped, sorted by
// created_at DESC, and rows past the 90-day window are excluded.
func TestMemStore_ListDeploymentScopeExclusions_SortedAndScoped(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	now := time.Now()

	// Insert three rows for proj-1 with descending created_at
	// (newest first). Insert two rows for proj-2 to confirm
	// scoping.
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-1",
		Slug: "alpha", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-3 * time.Hour),
	})
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-2",
		Slug: "beta", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-2 * time.Hour),
	})
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-3",
		Slug: "gamma", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-1 * time.Hour),
	})
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-2", AppID: "app-4",
		Slug: "delta", Reason: "", CreatedBy: "op",
	})
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-2", AppID: "app-5",
		Slug: "epsilon", Reason: "", CreatedBy: "op",
	})

	got, err := m.ListDeploymentScopeExclusions(ctx, "proj-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("list proj-1: got %d rows, want 3", len(got))
	}
	if got[0].Slug != "gamma" || got[1].Slug != "beta" || got[2].Slug != "alpha" {
		t.Errorf("list order: got [%s, %s, %s], want [gamma, beta, alpha] (DESC)",
			got[0].Slug, got[1].Slug, got[2].Slug)
	}

	got2, err := m.ListDeploymentScopeExclusions(ctx, "proj-2")
	if err != nil {
		t.Fatalf("list proj-2: %v", err)
	}
	if len(got2) != 2 {
		t.Errorf("list proj-2: got %d rows, want 2", len(got2))
	}
}

// TestMemStore_ListDeploymentScopeExclusions_FiltersOldRows pins
// the 90-day retention window — rows older than 90 days are
// filtered out by List. Mirrors the SQL partial index
// `WHERE created_at > now() - interval '90 days'`.
func TestMemStore_ListDeploymentScopeExclusions_FiltersOldRows(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	now := time.Now()

	// Fresh row (within window) — should appear.
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-1",
		Slug: "fresh", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-24 * time.Hour),
	})
	// Stale row (91 days old) — should be filtered.
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-2",
		Slug: "stale", Reason: "", CreatedBy: "op",
		CreatedAt: now.Add(-91 * 24 * time.Hour),
	})

	got, err := m.ListDeploymentScopeExclusions(ctx, "proj-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list: got %d rows, want 1 (stale filtered)", len(got))
	}
	if got[0].Slug != "fresh" {
		t.Errorf("list: got slug %q, want fresh", got[0].Slug)
	}
}

// TestMemStore_DeleteDeploymentScopeExclusion pins the
// (account, project, slug) composite-key delete. A refactor that
// dropped the slug from the WHERE clause would let one operator
// delete another operator's exclusion — the test catches that.
func TestMemStore_DeleteDeploymentScopeExclusion(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	mustCreateExclusion(t, m, memstoreExclusionSeed("proj-1"))

	if err := m.DeleteDeploymentScopeExclusion(ctx, "acct-1", "proj-1", "checkout-api"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Idempotent: a second delete returns ErrNotFound.
	if err := m.DeleteDeploymentScopeExclusion(ctx, "acct-1", "proj-1", "checkout-api"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("second delete: got %v, want ErrNotFound", err)
	}
	// Wrong account: ErrNotFound (no row matches).
	mustCreateExclusion(t, m, memstoreExclusionSeed("proj-1"))
	if err := m.DeleteDeploymentScopeExclusion(ctx, "wrong-acct", "proj-1", "checkout-api"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("wrong account: got %v, want ErrNotFound", err)
	}
}

// TestMemStore_LookupDeploymentScopeExclusions pins the
// (account, project) lookup the apply path uses. Returns rows
// sorted by created_at DESC; the handler folds each slug into the
// per-deploy exclude list when req.Exclude is empty.
func TestMemStore_LookupDeploymentScopeExclusions(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	now := time.Now()

	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-1",
		Slug: "older", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-2 * time.Hour),
	})
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-1", ProjectID: "proj-1", AppID: "app-2",
		Slug: "newer", Reason: "", CreatedBy: "op", CreatedAt: now.Add(-1 * time.Hour),
	})
	// Different account: must NOT appear.
	mustCreateExclusion(t, m, state.DeploymentScopeExclusion{
		AccountID: "acct-2", ProjectID: "proj-1", AppID: "app-3",
		Slug: "other-acct", Reason: "", CreatedBy: "op",
	})

	got, err := m.LookupDeploymentScopeExclusions(ctx, "acct-1", "proj-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("lookup: got %d rows, want 2", len(got))
	}
	if got[0].Slug != "newer" || got[1].Slug != "older" {
		t.Errorf("lookup order: got [%s, %s], want [newer, older]", got[0].Slug, got[1].Slug)
	}
}

// mustCreateExclusion is the test-only helper that fails the
// enclosing t on insert error so the sub-tests stay focused on
// the contract under test.
func mustCreateExclusion(t *testing.T, m *state.MemStore, in state.DeploymentScopeExclusion) {
	t.Helper()
	if _, err := m.CreateDeploymentScopeExclusion(context.Background(), in); err != nil {
		t.Fatalf("mustCreateExclusion: %v", err)
	}
}
