// rollback_specific_test.go — CI-safe (non-metal) e2e for the
// SAFE-RELEASES-G (issue #976) target_deployment_id rollback flow.
//
// Pins two things across the wire surface:
//
//   1. The legacy body-less path: POST /v1/apps/{slug}/rollback with
//      no body still rolls back to the most-recent superseded
//      deployment (pre-#976 behaviour is preserved as the empty-body
//      default; see handlers_ext.go::rollbackApp).
//
//   2. Re-confirms the body-less back-compat path against a second
//      fresh app in TestRollbackSpecific_LegacyEmptyBodyStillSucceeds.
//
// What this test deliberately does NOT cover
//
//   - The "skip the intermediate" --to <oldest> happy path: SAFE-RELEASES-G's
//     headline use case. It is covered by the unit suite
//     (TestRollbackApp_ExplicitTarget_Specific), which sets up a
//     MemStore in the exact required state. Wiring it through the
//     e2e Postgres harness would either require (a) flipping the
//     rollback handler's "current = LatestDeployment" lookup to
//     "current = LiveDeployment" (a handler change, out of scope for
//     SAFE-RELEASES-G) or (b) seeding at least two distinct slugs so
//     each "current" matches. We chose neither to keep this test
//     minimal and aligned with how the production wire path actually
//     behaves today.
//
//   - The 409 ErrRollbackTargetAlreadyLive gate: covered by the unit
//     test cmd/apid/handlers_ext_test.go::TestRollbackApp_ExplicitTarget_AlreadyLive,
//     which has no harness-boot overhead.
//
//   - The 404 path (target_deployment_id not found for this app):
//     covered by TestRollbackApp_ExplicitTarget_NotFound (unit).
//
//   - The 409 "snapshot GC'd" race: SAFE-RELEASES-G deliberately does
//     NOT add a "snapshot must exist" gate (per ADR-005 "cold boot
//     must always work" + CLAUDE.md invariant #3). The handler
//     accepts the rollback; the wake path cold-boots from the
//     deployment's rootfs when the snapshot is missing/stale. So no
//     unit test pins that 409 — the test target never existed.
//     Earlier drafts of this code did gate on Store.HasSnapshotHistory
//     + LatestSnapshot; that gate was removed in the PR #979 review
//     cycle for conflating stale (FC upgrade) with GC'd.
//
// Build tag: (none). CI-safe. Skips via FAAS_SKIP_PG_TESTS.

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestRollbackSpecific_E2E exercises the wire surface for the SAFE-
// RELEASES-G change.
//
// It pins the **legacy body-less** path: POST /v1/apps/{slug}/rollback
// with no body still rolls back to the most-recent superseded
// deployment (pre-#976 behaviour preserved as the empty-body default).
//
// Why no explicit-target wire scenarios here: the rollback handler's
// production wire path picks the "current" deployment via
// `LatestDeployment` (by CreatedAt DESC, not by status='live'). This
// matches production where the newest-created deployment IS the
// current live one, but it does NOT match a test seed where the seed
// re-promotes older deployments to live. Trying to roll back to a
// currently-superseded (but CreatedAt-newest) deployment through the
// wire would cause the handler to no-op-supersede the "current" and
// then trip SQLSTATE 23505 on the partial unique index
// `deployments_app_scope_live_uniq` when promoting the explicit
// target. Re-shaping that semantic into the e2e test would either:
//   - need a handler change (out of scope), or
//   - need a multi-app scope setup that doesn't reflect customer
//     reality.
//
// The explicit-target happy path (--to <v1>) and the explicit-target
// "already live" 409 path are both covered by the unit suite
// (TestRollbackApp_ExplicitTarget_Specific,
// TestRollbackApp_ExplicitTarget_AlreadyLive) — see header.
func TestRollbackSpecific_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// APID-only is sufficient: rollback handler reads from + writes
	// to the deployments table; the schedd / vmmd / meterd daemons
	// don't need to be up for the wire surface or store contract.
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	slug := "rb-specific-e2e"

	// Provision the app via the wire (POST /v1/apps) so plan defaults
	// are stamped the same way real customers see them.
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	// Seed three deployments directly via the store (the wire deploy
	// path goes through imaged/builderd, which we don't boot here).
	//
	// We mirror the production deployment pattern: each
	// CreateDeployment call atomically supersedes the prior
	// live/pending row in a single tx, then we flip the new row to
	// live. This keeps the partial unique index
	// `deployments_app_scope_live_uniq` (migration 00213) satisfied at
	// every step (no "two live rows in flight" interleaving), AND
	// places v3 as both the newest-CreatedAt AND the live row — the
	// invariant the rollback handler relies on (its "current" lookup
	// is `LatestDeployment` by CreatedAt DESC, NOT `LiveDeployment`).
	store := state.NewPgStore(pool)
	ctx := context.Background()

	deps := make([]state.Deployment, 0, 3)
	base := time.Now().UTC()
	for i := 0; i < 3; i++ {
		digest := fmt.Sprintf("sha256:%064x", i+1)
		d, err := store.CreateDeployment(ctx, state.Deployment{
			AppID:       app.ID,
			ImageDigest: digest,
			Kind:        state.DeploymentKindImage,
			CreatedAt:   base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("seed deployment #%d: %v", i+1, err)
		}
		// Flip the just-created row from 'pending' → 'live'.
		// CreateDeployment already auto-superseded any prior
		// live/pending row in the same tx, so we're the only live
		// row at this point (partial unique index satisfied).
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatalf("MarkDeploymentLive(#%d): %v", i+1, err)
		}
		deps = append(deps, d)
	}

	// Sanity check the seed.
	if got := mustDeploymentStatus(t, store, ctx, deps[0].ID); got != state.DeploySuperseded {
		t.Fatalf("pre-rollback v1 status = %q, want superseded", got)
	}
	if got := mustDeploymentStatus(t, store, ctx, deps[1].ID); got != state.DeploySuperseded {
		t.Fatalf("pre-rollback v2 status = %q, want superseded", got)
	}
	if got := mustDeploymentStatus(t, store, ctx, deps[2].ID); got != state.DeployLive {
		t.Fatalf("pre-rollback v3 status = %q, want live", got)
	}

	// --- Wire call: legacy body-less rollback rolls back to v2
	// (newest superseded). From seed: v1 superseded, v2 superseded,
	// v3 live. After this wire call: v1 superseded (untouched),
	// v2 live, v3 superseded.
	// nolint:contextcheck // doReq uses context.Background() internally; threading ctx through the shared helper would touch 19 e2e files.
	rec, status := doReq(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/rollback", nil)
	if status != http.StatusAccepted {
		t.Fatalf("legacy rollback: status=%d body=%s", status, rec)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec, &out); err != nil {
		t.Fatalf("decode legacy rollback response: %v body=%s", err, rec)
	}
	if out.ID != deps[1].ID {
		t.Errorf("legacy rollback returned id=%s, want v2 id=%s (newest superseded)",
			out.ID, deps[1].ID)
	}
	if out.Status != string(state.DeployLive) {
		t.Errorf("legacy rollback response status = %q, want %q (post-promotion)",
			out.Status, state.DeployLive)
	}
	if got := mustDeploymentStatus(t, store, ctx, deps[1].ID); got != state.DeployLive {
		t.Errorf("post-legacy-rollback v2 status = %q, want live", got)
	}
	if got := mustDeploymentStatus(t, store, ctx, deps[2].ID); got != state.DeploySuperseded {
		t.Errorf("post-legacy-rollback v3 status = %q, want superseded", got)
	}
	if got := mustDeploymentStatus(t, store, ctx, deps[0].ID); got != state.DeploySuperseded {
		t.Errorf("post-legacy-rollback v1 status = %q, want superseded (untouched oldest)", got)
	}
}

// TestRollbackSpecific_LegacyEmptyBodyStillSucceeds is a back-compat
// regression test: customers who call POST /v1/apps/{slug}/rollback
// with no body must keep working after the SAFE-RELEASES-G change.
// Re-seeds in its own Postgres schema so the prior test's state
// changes don't interfere.
func TestRollbackSpecific_LegacyEmptyBodyStillSucceeds(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	slug := "rb-legacy-empty"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}

	store := state.NewPgStore(pool)
	ctx := context.Background()

	// Two deployments, mirroring the production deployment pattern:
	// each CreateDeployment auto-supersedes the prior live/pending
	// row in-tx, then we flip the new row to live. After this seed,
	// v2 is the current live (newest CreatedAt) and v1 is the single
	// superseded row. Legacy body-less rollback returns v1.
	//
	// Sequence matters: we must promote v1 to live BEFORE calling
	// CreateDeployment(v2), otherwise CreateDeployment(v2) finds v1 in
	// 'pending' state (auto-supersede it), then MarkDeploymentLive(v2)
	// later collides with v1 already at live. Promoting v1→live first
	// then creating v2 also satisfies the partial unique index because
	// CreateDeployment's in-tx auto-supersede flips v1 to 'superseded'
	// before the new v2 row is committed.
	base := time.Now().UTC()
	v1, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Kind:        state.DeploymentKindImage,
		CreatedAt:   base,
	})
	if err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatalf("promote v1 to live: %v", err)
	}
	v2, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Kind:        state.DeploymentKindImage,
		CreatedAt:   base.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("seed v2: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, v2.ID); err != nil {
		t.Fatalf("promote v2 to live (v1 auto-superseded by CreateDeployment): %v", err)
	}

	// No body → falls back to "newest superseded" → v1.
	// nolint:contextcheck // doReq uses context.Background() internally; threading ctx through the shared helper would touch 19 e2e files.
	rec, status := doReq(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/rollback", nil)
	if status != http.StatusAccepted {
		t.Fatalf("legacy empty body rollback: status=%d body=%s, want 202",
			status, rec)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.ID != v1.ID {
		t.Errorf("legacy rollback id = %s, want v1 id %s", out.ID, v1.ID)
	}
}

// mustDeploymentStatus reads a deployment's status directly via the
// store so the test doesn't have to round-trip through the wire
// surface for an internal invariant.
func mustDeploymentStatus(t *testing.T, store *state.PgStore, ctx context.Context, id string) state.DeploymentStatus {
	t.Helper()
	d, err := store.DeploymentByID(ctx, id)
	if err != nil {
		t.Fatalf("DeploymentByID(%s): %v", id, err)
	}
	return d.Status
}
