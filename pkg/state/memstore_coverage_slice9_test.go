package state

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This slice closes the remaining real-branch gaps in MemStore that the
// earlier slices left at <100%: build-claim FIFO + terminal CAS, account
// provider-ID remapping, scan-source downgrade, and the empty/missing
// paths of the list helpers.

func TestMemStoreCoverageClaimNextQueuedBuild(t *testing.T) {
	m, ctx, _, _, deployment := memCoverageSlice4Fixture(t)
	// The slice4 fixture already has one queued build on the deployment;
	// add two more with deterministic, distinct EnqueuedAt values so
	// FIFO is observable (CreateBuild stamps EnqueuedAt=now, so we set
	// them directly on the internal map to avoid timestamp ties).
	queued1, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 10, "/tmp/q1.log")
	if err != nil {
		t.Fatal(err)
	}
	queued2, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 10, "/tmp/q2.log")
	if err != nil {
		t.Fatal(err)
	}
	// A running build must not be picked.
	running, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 10, "/tmp/q3.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimQueuedBuild(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	// Backdate the two queued rows so the order is deterministic.
	base := time.Now().Add(-time.Hour)
	m.mu.Lock()
	b1 := m.builds[queued1.ID]
	b1.EnqueuedAt = base
	m.builds[queued1.ID] = b1
	b2 := m.builds[queued2.ID]
	b2.EnqueuedAt = base.Add(time.Minute)
	m.builds[queued2.ID] = b2
	m.mu.Unlock()

	// FIFO: the earliest EnqueuedAt wins. queued1 (base) before queued2
	// (base+1m) before the fixture build (now).
	picked, err := m.ClaimNextQueuedBuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if picked.ID != queued1.ID || picked.Status != BuildRunning {
		t.Fatalf("picked = %s/%s, want %s/running", picked.ID, picked.Status, queued1.ID)
	}
	second, err := m.ClaimNextQueuedBuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != queued2.ID || second.Status != BuildRunning {
		t.Fatalf("second = %s/%s, want %s/running", second.ID, second.Status, queued2.ID)
	}
	// Queue still has the fixture build (created first, EnqueuedAt=now
	// at fixture time — strictly older than nothing, so it's next).
	if _, err := m.ClaimNextQueuedBuild(ctx); err != nil {
		t.Fatalf("fixture build claim = %v", err)
	}
	// Queue drained → ErrNotFound.
	if _, err := m.ClaimNextQueuedBuild(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("drained = %v", err)
	}
}

func TestMemStoreCoverageUpdateBuildStatusCAS(t *testing.T) {
	m, ctx, _, _, deployment := memCoverageSlice4Fixture(t)
	build, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 10, "/tmp/cas.log")
	if err != nil {
		t.Fatal(err)
	}
	// Terminal transition from queued (not running) → ErrNotFound.
	if err := m.UpdateBuildStatus(ctx, build.ID, BuildSucceeded, "", false, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminal from queued = %v", err)
	}
	// Flip to running, then succeed with failure class + timestamps.
	if _, err := m.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateBuildStatus(ctx, build.ID, BuildFailed, FailureTimeout, false, true); err != nil {
		t.Fatal(err)
	}
	got, err := m.BuildByID(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != BuildFailed || got.FailureClass != FailureTimeout || got.FinishedAt.IsZero() {
		t.Fatalf("failed build = %+v", got)
	}
	// Missing build → ErrNotFound.
	if err := m.UpdateBuildStatus(ctx, "missing", BuildSucceeded, "", false, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
	}
}

func TestMemStoreCoverageDomainsAndKeys(t *testing.T) {
	m, ctx, account, app, _ := memCoverageSlice4Fixture(t)
	// ListDomainsForApp populated + empty.
	dom, err := m.CreateCustomDomain(ctx, "app.example.com", app.ID, "tok")
	if err != nil {
		t.Fatal(err)
	}
	_ = dom
	if got, err := m.ListDomainsForApp(ctx, app.ID); err != nil || len(got) != 1 {
		t.Fatalf("domains for app = %+v, %v", got, err)
	}
	if got, err := m.ListDomainsForApp(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("domains missing = %+v, %v", got, err)
	}
	// API key: ListAPIKeys + TouchKeyLastUsed (hit/miss).
	hash := []byte("slice9-key-hash")
	key, err := m.CreateAPIKey(ctx, account.ID, hash, "slice9", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListAPIKeys(ctx, account.ID); err != nil || len(got) != 1 || got[0].ID != key.ID {
		t.Fatalf("list keys = %+v, %v", got, err)
	}
	if got, err := m.ListAPIKeys(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("list keys missing = %+v, %v", got, err)
	}
	if err := m.TouchKeyLastUsed(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.TouchKeyLastUsed(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch missing = %v", err)
	}
}

func TestMemStoreCoverageAccountProviderRemap(t *testing.T) {
	m, ctx, account, _, _ := memCoverageSlice4Fixture(t)
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "cus_1"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "cus_1"); err != nil || got.ID != account.ID {
		t.Fatalf("by provider = %+v, %v", got, err)
	}
	// Re-map to a new customer id — the old index entry must go.
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "cus_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AccountByProviderCustomerID(ctx, "cus_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old provider still resolvable = %v", err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "cus_2"); err != nil || got.ID != account.ID {
		t.Fatalf("new provider = %+v, %v", got, err)
	}
	// Missing account / missing provider.
	if err := m.UpdateAccountProviderCustomerID(ctx, "missing", "cus_3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
	}
	if _, err := m.AccountByProviderCustomerID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("by provider missing = %v", err)
	}
	// UpdateAccountStatus (hit/miss).
	if err := m.UpdateAccountStatus(ctx, account.ID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	acct, _ := m.AccountByID(ctx, account.ID)
	if acct.Status != AccountSuspended {
		t.Fatalf("status = %s", acct.Status)
	}
	if err := m.UpdateAccountStatus(ctx, "missing", AccountActive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status missing = %v", err)
	}
	// AuthenticateKey — missing hash.
	if _, _, err := m.AuthenticateKey(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("authenticate missing = %v", err)
	}
	// AccountByID — missing.
	if _, err := m.AccountByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("account missing = %v", err)
	}
}

func TestMemStoreCoverageSetProjectScanSourceDowngrade(t *testing.T) {
	m, ctx, account, _, _ := memCoverageSlice4Fixture(t)
	proj, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "scan-proj", ScanSource: ProjectScanSourceConvention})
	if err != nil {
		t.Fatal(err)
	}
	// Upgrade → allowed.
	if got, err := m.SetProjectScanSource(ctx, proj.ID, ProjectScanSourceCompose); err != nil || got.ScanSource != ProjectScanSourceCompose {
		t.Fatalf("upgrade = %+v, %v", got, err)
	}
	// Downgrade → ErrScanSourceDowngrade.
	if _, err := m.SetProjectScanSource(ctx, proj.ID, ProjectScanSourceSingle); !errors.Is(err, ErrScanSourceDowngrade) {
		t.Fatalf("downgrade = %v", err)
	}
	// Same tier → no-op.
	if got, err := m.SetProjectScanSource(ctx, proj.ID, ProjectScanSourceCompose); err != nil || got.ScanSource != ProjectScanSourceCompose {
		t.Fatalf("same tier = %+v, %v", got, err)
	}
	// Missing project.
	if _, err := m.SetProjectScanSource(ctx, "missing", ProjectScanSourceSingle); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project = %v", err)
	}
}

func TestMemStoreCoverageDeleteProjectWithRepo(t *testing.T) {
	m, ctx, account, _, _ := memCoverageSlice4Fixture(t)
	proj, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "repo-proj", InstallID: 55, RepoFullName: "acme/repo"})
	if err != nil {
		t.Fatal(err)
	}
	// DeleteProject must drop the by-(install, repo) index.
	if err := m.DeleteProject(ctx, proj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProjectByRepo(ctx, "", 55, "acme/repo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repo lookup after delete = %v", err)
	}
	// A standalone project (no install) also deletes cleanly.
	standalone, err := m.CreateProject(ctx, Project{AccountID: account.ID, Slug: "standalone-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProject(ctx, standalone.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemStoreCoverageListUnplacedAndAllApps(t *testing.T) {
	m, ctx, _, app, _ := memCoverageSlice4Fixture(t)
	// The fixture app has no node owner → unplaced.
	if got, err := m.ListUnplacedApps(ctx); err != nil || len(got) != 1 || got[0].ID != app.ID {
		t.Fatalf("unplaced = %+v, %v", got, err)
	}
	// Claim it → no longer unplaced.
	if err := m.SetAppNodeID(ctx, app.ID, DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListUnplacedApps(ctx); err != nil || len(got) != 0 {
		t.Fatalf("unplaced after claim = %+v, %v", got, err)
	}
	// ListAllApps includes deleted apps? It excludes them — soft-delete
	// and confirm the count drops.
	if got, err := m.ListAllApps(ctx); err != nil || len(got) != 1 {
		t.Fatalf("all apps = %+v, %v", got, err)
	}
	if _, err := m.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListAllApps(ctx); err != nil || len(got) != 0 {
		t.Fatalf("all apps after delete = %+v, %v", got, err)
	}
	// ListAppsByNodeID — after soft-delete the app is excluded; assert
	// empty.
	if got, err := m.ListAppsByNodeID(ctx, DefaultLocalNodeName); err != nil || len(got) != 0 {
		t.Fatalf("apps by node = %+v, %v", got, err)
	}
}

// TestMemStoreCoverageListBuildsForAccountPaged (ADR-091, issue #741
// close-out) covers the paged list path. Mirrors the PgStore
// keyset SQL via a slice filter + sort. Pins:
//
//  1. Account-scoped default (no app/status filter).
//  2. ?app=ID filter narrows to one app.
//  3. ?status=running filter excludes queued + succeeded.
//  4. Keyset cursor (before=...) pages backwards through
//     started_at desc nulls last — queued (zero StartedAt)
//     rows stay at the bottom of the first page and drop off
//     once before is set.
//  5. limit clamps the result set.
//  6. Cross-account data never surfaces (GDPR-export-style
//     isolation is the SQL store's job; the memstore mimics it
//     via the apps.AccountID join).
func TestMemStoreCoverageListBuildsForAccountPaged(t *testing.T) {
	m, ctx, account, _, deployment := memCoverageSlice4Fixture(t)
	// Second app + deployment under the same account for the
	// cross-app filter.
	app2, err := m.CreateApp(ctx, App{ID: "00000000-0000-0000-0000-00000000slice9b", AccountID: account.ID, Slug: "slice9b"})
	if err != nil {
		t.Fatalf("CreateApp app2: %v", err)
	}
	dep2, err := m.CreateDeployment(ctx, Deployment{ID: "00000000-0000-0000-0000-00000000slice9d", AppID: app2.ID, Kind: DeploymentKindImage, CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("CreateDeployment dep2: %v", err)
	}
	// Third account — its build must NEVER surface, even with
	// no filter set.
	otherAcct, err := m.CreateAccount(ctx, "slice9-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount other: %v", err)
	}
	otherApp, err := m.CreateApp(ctx, App{ID: "00000000-0000-0000-0000-00000000slice9c", AccountID: otherAcct.ID, Slug: "slice9c"})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	otherDep, err := m.CreateDeployment(ctx, Deployment{ID: "00000000-0000-0000-0000-00000000slice9e", AppID: otherApp.ID, Kind: DeploymentKindImage, CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("CreateDeployment other: %v", err)
	}

	// Seed 5 builds across the two owned deployments with
	// deterministic, distinct started_at values, plus one queued
	// (zero StartedAt) per deployment, plus one cross-account
	// build.
	base := time.Now().Add(-time.Hour)
	ownedBuilds := []Build{}
	for i := 0; i < 5; i++ {
		b, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 100, "/tmp/p.log")
		if err != nil {
			t.Fatalf("CreateBuild owned[%d]: %v", i, err)
		}
		// Backdate + claim + mark running so StartedAt sticks.
		m.mu.Lock()
		row := m.builds[b.ID]
		row.StartedAt = base.Add(time.Duration(i) * time.Minute)
		m.builds[b.ID] = row
		m.mu.Unlock()
		if _, err := m.ClaimQueuedBuild(ctx, b.ID); err != nil {
			t.Fatalf("ClaimQueuedBuild[%d]: %v", i, err)
		}
		// After ClaimQueuedBuild the row's StartedAt may have been
		// re-stamped; restore the deterministic value.
		m.mu.Lock()
		row = m.builds[b.ID]
		row.StartedAt = base.Add(time.Duration(i) * time.Minute)
		m.builds[b.ID] = row
		m.mu.Unlock()
		ownedBuilds = append(ownedBuilds, m.builds[b.ID])
	}
	// 1 queued build on the second owned deployment — zero
	// StartedAt, must surface at the BOTTOM of the account-wide
	// first page and drop off once `before` is set.
	queued2, err := m.CreateBuild(ctx, dep2.ID, DeploymentKindImage, 50, "/tmp/q2.log")
	if err != nil {
		t.Fatalf("CreateBuild queued2: %v", err)
	}
	_ = queued2
	// 1 build on the other-account deployment — must never surface.
	_, err = m.CreateBuild(ctx, otherDep.ID, DeploymentKindImage, 999, "/tmp/x.log")
	if err != nil {
		t.Fatalf("CreateBuild other: %v", err)
	}

	// (1) Account-scoped, no filter — returns all 7 owned builds
	// ordered started_at desc nulls last, id desc as tiebreaker,
	// queued (zero) at the bottom. (5 running seeded above + 1
	// queued on dep2 + 1 fixture queued on dep1 from
	// memCoverageSlice4Fixture.)
	got, err := m.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("account-wide: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("account-wide len = %d, want 7 (cross-account row leaked or count drift): %+v", len(got), got)
	}
	// Top row = newest owned (base+4m); bottom 2 = queued (zero)
	// rows (slice4 fixture + dep2 queued).
	if !got[0].StartedAt.Equal(base.Add(4 * time.Minute)) {
		t.Errorf("top row started_at = %v, want %v", got[0].StartedAt, base.Add(4*time.Minute))
	}
	queuedCount := 0
	for _, b := range got {
		if b.StartedAt.IsZero() {
			queuedCount++
		}
	}
	if queuedCount != 2 {
		t.Errorf("queued rows = %d, want 2 (fixture + dep2)", queuedCount)
	}

	// (2) app=app2.ID — only the queued build on dep2 qualifies.
	got, err = m.ListBuildsForAccountPaged(ctx, account.ID, "", app2.ID, time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("app filter: %v", err)
	}
	if len(got) != 1 || !got[0].StartedAt.IsZero() || got[0].DeploymentID != dep2.ID {
		t.Fatalf("app filter = %+v, want 1 queued on dep2", got)
	}

	// (3) status=running — only the 5 claimed builds qualify
	// (queued has status=queued, not running).
	got, err = m.ListBuildsForAccountPaged(ctx, account.ID, "running", "", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("status filter: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("status running len = %d, want 5", len(got))
	}
	for _, b := range got {
		if b.Status != BuildRunning {
			t.Errorf("status filter leaked %s/%s", b.ID, b.Status)
		}
	}

	// (4) Keyset cursor — page 1 with limit=3 returns the 3
	// newest running (base+4m, +3m, +2m); the cursor (base+2m +
	// page1[2].ID) pages backwards to the 2 remaining running
	// (+1m, base) + 1 queued at the tail (dep2's queued build
	// — the id tiebreaker keeps it deterministic). The slice4
	// fixture's queued row dropped off because its ID sorts AFTER
	// page1[2].ID when both have zero started_at.
	page1, err := m.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, "", 3)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1 len = %d, want 3", len(page1))
	}
	if !page1[2].StartedAt.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("page1[2] = %v, want base+2m", page1[2].StartedAt)
	}
	// Pass the (started_at, id) tuple as the cursor — the id
	// tiebreaker is what lets the queued tail surface.
	page2, err := m.ListBuildsForAccountPaged(ctx, account.ID, "", "",
		page1[2].StartedAt, page1[2].ID, 3)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2 len = %d, want 3 (queued at tail): %+v", len(page2), page2)
	}
	// page2[0..1] = base+1m, base. page2[2] = queued (zero)
	// from dep2 (the one we just created).
	if !page2[0].StartedAt.Equal(base.Add(time.Minute)) {
		t.Errorf("page2[0] = %v, want base+1m", page2[0].StartedAt)
	}
	if !page2[1].StartedAt.Equal(base) {
		t.Errorf("page2[1] = %v, want base", page2[1].StartedAt)
	}
	if !page2[2].StartedAt.IsZero() {
		t.Errorf("page2[2] started_at = %v, want zero (queued)", page2[2].StartedAt)
	}

	// Page 3: cursor is the queued row's id (zero started_at) —
	// the queued tail's id-only keyset picks up the slice4 fixture's
	// queued row.
	page3, err := m.ListBuildsForAccountPaged(ctx, account.ID, "", "",
		page2[2].StartedAt, page2[2].ID, 3)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len = %d, want 1 (fixture queued): %+v", len(page3), page3)
	}
	if !page3[0].StartedAt.IsZero() {
		t.Errorf("page3[0] started_at = %v, want zero", page3[0].StartedAt)
	}

	// (5) limit clamps — limit=2 returns 2 rows.
	clamped, err := m.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("limit: %v", err)
	}
	if len(clamped) != 2 {
		t.Errorf("limit=2 len = %d, want 2", len(clamped))
	}

	// (6) Cross-account isolation — querying the OTHER account
	// returns only the cross-account build, never the owned ones.
	got, err = m.ListBuildsForAccountPaged(ctx, otherAcct.ID, "", "", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("other account: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("other account len = %d, want 1 (cross-tenant leak?): %+v", len(got), got)
	}
	if got[0].DeploymentID != otherDep.ID {
		t.Errorf("other account leaked row: %s", got[0].DeploymentID)
	}

	// (7) Sanity: ordering invariant for the unfiltered set — sort
	// the owned running rows by started_at desc and confirm
	// ListBuildsForAccountPaged returns them at the TOP of the
	// result in the same order. Queued rows (zero StartedAt) sort
	// to the bottom — their relative order is not asserted.
	expected := append([]Build{}, ownedBuilds...)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].StartedAt.IsZero() {
			return false
		}
		if expected[j].StartedAt.IsZero() {
			return true
		}
		return expected[i].StartedAt.After(expected[j].StartedAt)
	})
	got, err = m.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("ordering invariant: %v", err)
	}
	// got[0..4] should equal expected[0..4] (the 5 running rows);
	// got[5..6] are queued (zero StartedAt) in arbitrary order.
	for i := 0; i < 5; i++ {
		if got[i].ID != expected[i].ID {
			t.Errorf("order[%d]: got %s, want %s", i, got[i].ID, expected[i].ID)
		}
		if got[i].StartedAt.IsZero() {
			t.Errorf("order[%d] leaked queued to top of running block", i)
		}
	}
}
