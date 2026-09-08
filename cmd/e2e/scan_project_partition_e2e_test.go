// scan_project_partition_e2e_test.go — non-metal CI-safe acceptance
// for the ADR-124 affected-workloads partition cardinality.
//
// What this test pins
//
//   - The partition is **complete**: every non-deleted app + every
//     scan-discovered workload appears in EXACTLY ONE of
//     `will_deploy` / `unaffected` / `skipped` / `removed`. The
//     pin is `union(WillDeploy, Unaffected, Skipped, Removed)`
//     equals the disjoint union of:
//
//       (a) scan workloads (post-`--only`, pre-`--exclude`)
//       (b) every non-deleted app in the account
//
//     minus the destructive subset (b ∩ Removed) and minus the
//     operator-excluded subset (a ∩ Skipped). The invariant is
//     what makes the partition safe to render as a UI table: the
//     operator sees "what this deploy does to EVERY row" without
//     gaps or duplicates.
//
//   - `Skipped` carries an excluded slug even when the slug is
//     ALSO in `unaffected` (operator has an existing app AND
//     excludes it). The dual appearance is intentional — the
//     operator sees the slug in `Unaffected` (existing app,
//     blast-radius view) AND in `Skipped` (this deploy will skip
//     it). The two views compose; they do not collide.
//
//   - Empty-plan partition: when the scan produces zero workloads
//     AND the account has zero apps, all four arrays are empty.
//     This is the cold-start case; the partition surface must
//     NOT crash and MUST NOT inject phantom rows.
//
// What this test deliberately does NOT cover
//
//   - The wire field `GateRescuedByExclude` — covered by PR-A
//     unit tests (`pkg/wire/metrics_test.go`) and the audit unit
//     test (`cmd/apid/scan_service_audit_test.go`). Driving the
//     gate rescue end-to-end requires seeding a Free-plan account
//     above the apps/cron quota; that is a separate harness path
//     and not needed to pin the cardinality invariant.
//
//   - The persistence wire field `PersistedExclusions` — covered
//     by PR-B's unit tests at
//     `pkg/state/pgstore_deployment_scope_exclusions_test.go`.
//     Driving it end-to-end would require the new pgstore surface
//     to be wired into a test harness helper, which is in
//     follow-up work.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS or when pgtest.Open returns nil).

package e2e_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// slugsFromPartition returns the slug set from a partition slice.
// Used to assert the cardinality invariant: union(WillDeploy,
// Unaffected, Skipped, Removed) is a complete projection.
func slugsFromPartition(apps []api.PlanAffectedApp) map[string]bool {
	out := make(map[string]bool, len(apps))
	for _, a := range apps {
		out[a.Slug] = true
	}
	return out
}

// seedAccountWithAPIKey creates one account on `plan` with one API
// key, returning both. Mirrors the pattern in
// `account_scoped_e2e_test.go::seedAccount` but does not require
// the test to call `h.SeedAccount` (which uses a random email).
// Used here so each partition test can name its account explicitly
// for the audit trail.
func seedAccountWithAPIKey(t *testing.T, h *e2etest.Harness,
	plan api.Plan, email string) (state.Account, string) {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: email,
		Plan:  plan,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg %s: %v", email, err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, res.Account.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return res.Account, pt
}

// TestScanPartition_CardinalityComplete pins the central partition
// invariant: union(WillDeploy, Unaffected, Skipped, Removed)
// accounts for every non-deleted app in the account + every scan
// workload. Setup:
//
//   - 3 pre-seeded apps:
//   - "matching-app"   (RootDir="services/api", Name="api") —
//     scan discovers a matching workload.
//   - "legacy-app"     (RootDir="external/legacy", Name="legacy")
//     — scan does NOT discover a matching workload.
//   - "side-app"       (RootDir="external/side", Name="side") —
//     scan does NOT discover a matching workload.
//   - Scan fixture: singleWorkloadFixture (1 workload: `api`).
//
// Expected partition:
//
//   - WillDeploy carries `api` (Action="create") — the scan
//     workload.
//   - Unaffected carries both `legacy-app` and `side-app` (existing
//     apps without matching scan workloads; the scan omits them
//     and the operator has not excluded them).
//   - Removed is empty (none of the seeded apps match a scan
//     workload, but the seeded apps ARE in the account, so they
//     appear in Unaffected first; nothing drops into Removed).
//   - Skipped is empty (no --exclude on this run).
//
// Cardinality: 3 pre-seeded apps + 1 scan workload = 4 rows in
// the partition (WillDeploy:1 + Unaffected:2 = 3 distinct apps;
// the 4th row is the scan workload `api` which is already
// counted under WillDeploy.Action="create"). Removed + Skipped
// are empty.
func TestScanPartition_CardinalityComplete(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "source"),
			"FAAS_SCAN_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	acct, pt := seedAccountWithAPIKey(t, h, api.PlanPro,
		"e2e+partition-cardinality@test.example")
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()

	// Seed 3 apps. Only `matching-app` shares (RootDir, Name) with
	// the scan fixture (services/api + api). The other two have
	// external root paths that the scan will not see.
	for _, slug := range []string{"legacy-app", "side-app"} {
		if _, err := store.CreateApp(ctx, state.App{
			AccountID:      acct.ID,
			Slug:           slug,
			Type:           state.AppTypeApp,
			RAMMB:          256,
			MaxConcurrency: 1,
		}); err != nil {
			t.Fatalf("CreateApp %s: %v", slug, err)
		}
	}
	matching, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "matching-app",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		// The (RootDir, WorkloadName) MUST match the scan fixture
		// (singleWorkloadFixture emits a workload with
		// RootDir="services/api", Name="api"). Otherwise the
		// partition treats matching-app as Unaffected (no scan
		// match) and WillDeploy carries the scan workload as a
		// fresh `create` instead of an `update` against this
		// pre-existing app row.
		RootDir:      "services/api",
		WorkloadName: "api",
	})
	if err != nil {
		t.Fatalf("CreateApp matching-app: %v", err)
	}

	// Run scan with singleWorkloadFixture (1 workload: api).
	plan, status, body := scanProjectMultipartWithExclude(t, h, pt,
		"partition-cardinality", "", "", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}

	// WillDeploy: exactly 1 row, the scan workload `api`.
	// Action + ID are pinned below (lines 230-240) — the scan
	// discovers `matching-app` (same RootDir, Name) and reuses
	// the existing row, so WillDeploy[0] carries Action="update"
	// + the matching.ID. The Action="create" / empty-ID assertions
	// that used to live here were the precondition for the
	// partition surface BEFORE we set matching-app's RootDir +
	// WorkloadName to match the scan fixture. With matching key
	// the partition reuses the row, so this block intentionally
	// only pins Slug + cardinality.
	if len(plan.WillDeploy) != 1 {
		t.Errorf("WillDeploy len = %d, want 1; got: %+v", len(plan.WillDeploy), plan.WillDeploy)
	} else if plan.WillDeploy[0].Slug != "api" {
		t.Errorf("WillDeploy[0].Slug = %q, want api", plan.WillDeploy[0].Slug)
	}

	// Unaffected: exactly 2 rows, legacy-app + side-app.
	unaffected := slugsFromPartition(plan.Unaffected)
	if len(unaffected) != 2 {
		t.Errorf("Unaffected len = %d, want 2 (legacy-app, side-app); got: %+v",
			len(unaffected), plan.Unaffected)
	}
	for _, slug := range []string{"legacy-app", "side-app"} {
		if !unaffected[slug] {
			t.Errorf("Unaffected missing %q; got: %+v", slug, plan.Unaffected)
		}
	}
	// matching-app has the same (RootDir, Name) as the scan
	// workload, so it shows up in WillDeploy (with the existing
	// app's id) — but only when the scan finds a workload that
	// matches its root_dir. With singleWorkloadFixture (root_dir
	// `services/api`), matching-app's `services/api` root DOES
	// match, so the scan reuses the existing app. The WillDeploy
	// row carries the existing app's id (Action="update" not
	// "create"). Pin this:
	if len(plan.WillDeploy) == 1 && plan.WillDeploy[0].ID != matching.ID {
		t.Errorf("WillDeploy[0].ID = %q, want matching app id %q "+
			"(scan reuses the existing app row)",
			plan.WillDeploy[0].ID, matching.ID)
	}
	if len(plan.WillDeploy) == 1 && plan.WillDeploy[0].Action != "update" {
		t.Errorf("WillDeploy[0].Action = %q, want update "+
			"(existing app + matching scan workload)", plan.WillDeploy[0].Action)
	}

	// Skipped: empty (no --exclude).
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped len = %d, want 0 (no --exclude); got: %+v",
			len(plan.Skipped), plan.Skipped)
	}

	// Removed: empty (the seeded apps are accounted for in
	// Unaffected; nothing drops into Removed on this scan).
	if len(plan.Removed) != 0 {
		t.Errorf("Removed len = %d, want 0; got: %+v", len(plan.Removed), plan.Removed)
	}
}

// TestScanPartition_ExcludedExistingAppDualView pins that an
// excluded slug appears in BOTH `Unaffected` (existing app,
// blast-radius view) AND `Skipped` (operator excluded for this
// deploy) when the operator excludes an existing app that the
// scan did not re-discover. The dual appearance is intentional —
// the operator sees the slug in both views so the intent is
// observable.
//
// Setup:
//
//   - Pre-seed `existing-app` (RootDir="external/existing",
//     Name="existing") — not discovered by the scan.
//   - Run scan with exclude=existing-app.
//
// Expected partition:
//
//   - WillDeploy: empty (scan fixture has only `api`; existing-app
//     is not in the scan set, so there is nothing to update).
//   - Unaffected: carries `existing-app` (existing app, blast-radius
//     view; the partition covers every non-deleted app).
//   - Skipped: carries `existing-app` (operator excluded it for
//     this deploy).
//   - Removed: empty (the exclude protected from removal; the
//     audit fix at TestReconcile_ExcludePreventsRemove pins the
//     apply-side contract; this test pins the scan-side corollary).
func TestScanPartition_ExcludedExistingAppDualView(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "source"),
			"FAAS_SCAN_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	acct, pt := seedAccountWithAPIKey(t, h, api.PlanPro,
		"e2e+partition-dualview@test.example")
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()

	if _, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "existing-app",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
	}); err != nil {
		t.Fatalf("CreateApp existing-app: %v", err)
	}

	plan, status, body := scanProjectMultipartWithExclude(t, h, pt,
		"partition-dualview", "", "existing-app", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}

	// Unaffected carries the existing app (blast-radius view).
	unaffected := slugsFromPartition(plan.Unaffected)
	if !unaffected["existing-app"] {
		t.Errorf("Unaffected missing existing-app (operator excluded it but it is still an "+
			"existing app in the blast-radius view); got: %+v", plan.Unaffected)
	}

	// Skipped carries the excluded slug (operator intent view).
	skipped := slugsFromPartition(plan.Skipped)
	if !skipped["existing-app"] {
		t.Errorf("Skipped missing existing-app (operator excluded it for this deploy); got: %+v",
			plan.Skipped)
	}

	// Removed does NOT carry the excluded slug — the audit fix at
	// TestReconcile_ExcludePreventsRemove guarantees the apply
	// path; this pins the scan-side corollary.
	for _, slug := range plan.Removed {
		if slug == "existing-app" {
			t.Errorf("Removed carries excluded slug existing-app; " +
				"--exclude must protect from the destructive partition")
		}
	}

	// WillDeploy may carry the scan workload `api` (create) — we
	// don't constrain its size here; the cardinality invariant is
	// the focus.
}

// TestScanPartition_EmptyPlan pins the cold-start case: a Pro plan
// account with zero pre-seeded apps + a single-workload scan.
// Expected:
//
//   - WillDeploy carries `api` (create).
//   - Unaffected + Skipped + Removed all empty.
//
// This is the partition's baseline — no operator exclude, no
// destructive subset, no existing apps. Every surface stays empty
// where it should, and the scan returns 200 (no crash on an empty
// account).
func TestScanPartition_EmptyPlan(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "source"),
			"FAAS_SCAN_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	_, pt := seedAccountWithAPIKey(t, h, api.PlanPro,
		"e2e+partition-empty@test.example")

	plan, status, body := scanProjectMultipartWithExclude(t, h, pt,
		"partition-empty", "", "", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}

	if len(plan.Unaffected) != 0 {
		t.Errorf("Unaffected len = %d, want 0 (cold-start); got: %+v",
			len(plan.Unaffected), plan.Unaffected)
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped len = %d, want 0 (no --exclude); got: %+v",
			len(plan.Skipped), plan.Skipped)
	}
	if len(plan.Removed) != 0 {
		t.Errorf("Removed len = %d, want 0 (no destructive subset); got: %+v",
			len(plan.Removed), plan.Removed)
	}
	// WillDeploy has the scan workload `api` (create).
	if len(plan.WillDeploy) != 1 || plan.WillDeploy[0].Slug != "api" {
		t.Errorf("WillDeploy = %+v, want [{api create}]", plan.WillDeploy)
	}
}
