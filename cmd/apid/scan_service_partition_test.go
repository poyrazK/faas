package main

// scan_service_partition_test.go — unit tests for the ADR-124
// blast-radius partition (computeAffectedPartition). The preview-
// time Removed projection is the load-bearing operator-safety
// surface this PR closes; the tests pin:
//   1. project-scoping (multi-project accounts must not see other
//      projects' apps in the destructive subset);
//   2. exclude-honoring (operator --exclude keeps the matching app
//      alive, not soft-deleted);
//   3. scan-key matching (apps whose (RootDir, WorkloadName) is in
//      the scan set go to WillDeploy, not Removed).
//
// Equivalence with apply-path removedSlugs is covered by the
// e2e suite (cmd/e2e/apply_project_inputs_e2e_test.go): the
// apply-path removedSlugs is built from rec.Removed IDs resolved
// via AppsForProject, so a passing partition test here plus a
// passing e2e apply implies the partition-applied preview and
// the partition-applied apply produce the same Removed set.

import (
	"reflect"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectID is a shared fixture: the project being previewed.
// The partition's project-scoping Removed loop only includes
// apps whose ProjectID == projectID.
const projectID = "11111111-1111-1111-1111-111111111111"

// otherProjectID is a decoy — apps in this project must NOT
// appear in the Removed projection even when their (RootDir,
// WorkloadName) is not in the scan set.
const otherProjectID = "22222222-2222-2222-2222-222222222222"

// fixtureApps returns a stable app roster: two apps in the
// target project (one matched by scan, one not) and one app
// in another project (must be excluded from Removed).
func fixtureApps() []state.App {
	return []state.App{
		{ID: "app-matched", Slug: "matched", WorkloadName: "matched", RootDir: "apps", ProjectID: projectID},
		{ID: "app-orphan", Slug: "orphan", WorkloadName: "orphan", RootDir: "apps", ProjectID: projectID},
		{ID: "app-cross", Slug: "cross-project", WorkloadName: "cross-project", RootDir: "apps", ProjectID: otherProjectID},
	}
}

// fixtureScanWl returns a single-workload scan that matches
// `matched` but not `orphan` or `cross-project`. The
// exclude map is empty by default.
func fixtureScanWl() []reposcan.Workload {
	return []reposcan.Workload{{Name: "matched", RootDir: "apps"}}
}

// TestComputeAffectedPartition_RemovedProjectScoped pins gap #1's
// load-bearing project-scoping rule: an account app whose
// ProjectID differs from the previewed project must NOT appear
// in the Removed slice. Multi-project accounts would otherwise
// see their other projects' apps in the destructive preview,
// and reconcile would NOT actually delete them — a misleading
// "this scan will soft-delete these apps" message.
func TestComputeAffectedPartition_RemovedProjectScoped(t *testing.T) {
	apps := fixtureApps()
	scanWl := fixtureScanWl()

	got := computeAffectedPartition(scanWl, scanWl, apps, nil, projectID)

	wantRemoved := []string{"orphan"}
	if !reflect.DeepEqual(got.Removed, wantRemoved) {
		t.Fatalf("Removed project-scoping:\n  got:  %#v\n  want: %#v", got.Removed, wantRemoved)
	}

	// Also assert the cross-project app is NOT in WillDeploy or
	// Skipped (those are also project-scoped — see ADR §3 action
	// vocabulary "create/update vs noop" applied only to the
	// project's own workloads). It WILL appear in Unaffected;
	// that asymmetry is pinned by TestComputeAffectedPartition_
	// UnaffectedStaysAccountScoped below.
	for _, row := range got.WillDeploy {
		if row.ID == "app-cross" {
			t.Fatalf("cross-project app leaked into WillDeploy: %+v", row)
		}
	}
	for _, row := range got.Skipped {
		if row.ID == "app-cross" {
			t.Fatalf("cross-project app leaked into Skipped: %+v", row)
		}
	}
}

// TestComputeAffectedPartition_RemovedHonorsExclude pins the
// exclude-honoring rule: an existing app whose lowercased
// WorkloadName is in the operator's exclude set must NOT appear
// in Removed. The operator typed `--exclude=foo` to keep `foo`
// alive; if the partition surfaced `foo` in Removed, the apply
// path would SoftDeleteAppCascade it against the operator's
// intent — silent data loss.
func TestComputeAffectedPartition_RemovedHonorsExclude(t *testing.T) {
	apps := []state.App{
		{ID: "app-orphan", Slug: "orphan", WorkloadName: "orphan", RootDir: "apps", ProjectID: projectID},
	}
	scanWl := []reposcan.Workload{}

	// Without exclude: orphan is in Removed.
	noExcl := computeAffectedPartition(scanWl, scanWl, apps, nil, projectID)
	if !reflect.DeepEqual(noExcl.Removed, []string{"orphan"}) {
		t.Fatalf("baseline (no exclude): got %#v, want [orphan]", noExcl.Removed)
	}

	// With exclude=orphan: orphan must NOT be in Removed.
	exclude := map[string]bool{"orphan": true}
	withExcl := computeAffectedPartition(scanWl, scanWl, apps, exclude, projectID)
	if len(withExcl.Removed) != 0 {
		t.Fatalf("operator --exclude ignored: got Removed=%#v, want []", withExcl.Removed)
	}
}

// TestComputeAffectedPartition_RemovedSkipsScanKeyMatches pins
// the scan-key matching rule: an account app whose (RootDir,
// WorkloadName) is in the scan set belongs in WillDeploy (with
// Action=update), not in Removed. Mirrors pkg/reconcile.diff.
// workloadDiff:80-104 where same-key apps are updates, not
// removes.
func TestComputeAffectedPartition_RemovedSkipsScanKeyMatches(t *testing.T) {
	apps := []state.App{
		{ID: "app-matched", Slug: "matched", WorkloadName: "matched", RootDir: "apps", ProjectID: projectID},
	}
	scanWl := []reposcan.Workload{{Name: "matched", RootDir: "apps"}}

	got := computeAffectedPartition(scanWl, scanWl, apps, nil, projectID)

	// Matched key → WillDeploy (action=update), not Removed.
	if len(got.Removed) != 0 {
		t.Fatalf("scan-key-matched app in Removed: got %#v, want []", got.Removed)
	}
	if len(got.WillDeploy) != 1 || got.WillDeploy[0].Slug != "matched" || got.WillDeploy[0].Action != "update" {
		t.Fatalf("scan-key-matched app not in WillDeploy (update): got %#v", got.WillDeploy)
	}
}

func TestComputeAffectedPartition_DirectoryMoveIsUpdate(t *testing.T) {
	t.Parallel()
	apps := []state.App{{
		ID: "api-id", Slug: "api", WorkloadName: "api", RootDir: "services/api", ProjectID: projectID,
	}}
	scan := []reposcan.Workload{{Name: "api", RootDir: "apps/api"}}
	got := computeAffectedPartition(scan, scan, apps, nil, projectID)
	if len(got.WillDeploy) != 1 || got.WillDeploy[0].Action != "update" || got.WillDeploy[0].ID != "api-id" ||
		got.WillDeploy[0].ExistingRootDir != "services/api" || len(got.Removed) != 0 {
		t.Fatalf("directory move partition = %#v", got)
	}
}

func TestComputeAffectedPartition_OnlyDoesNotRemoveUnselectedWorkload(t *testing.T) {
	t.Parallel()
	apps := []state.App{
		{ID: "api-id", Slug: "api", WorkloadName: "api", RootDir: "services/api", ProjectID: projectID},
		{ID: "worker-id", Slug: "worker", WorkloadName: "worker", RootDir: "services/worker", ProjectID: projectID},
	}
	all := []reposcan.Workload{{Name: "api", RootDir: "services/api"}, {Name: "worker", RootDir: "services/worker"}}
	selected := all[:1]
	got := computeAffectedPartition(selected, all, apps, nil, projectID)
	if len(got.Removed) != 0 || len(got.WillDeploy) != 1 || got.WillDeploy[0].Slug != "api" {
		t.Fatalf("--only partition = %#v", got)
	}
}

// TestComputeAffectedPartition_BrandNewProjectSkipsRemoved pins
// the empty-projectID guard: a brand-new project (not yet
// inserted, so its ID is unknown) must yield an empty Removed
// slice. No existing app can have ProjectID == "", and we
// cannot predict removals without a project scope.
func TestComputeAffectedPartition_BrandNewProjectSkipsRemoved(t *testing.T) {
	apps := fixtureApps() // includes app-cross in otherProjectID
	scanWl := fixtureScanWl()

	// projectID="" → brand-new project, no apps in scope.
	got := computeAffectedPartition(scanWl, scanWl, apps, nil, "")

	if len(got.Removed) != 0 {
		t.Fatalf("brand-new project produced Removed: got %#v, want []", got.Removed)
	}
}

// TestComputeAffectedPartition_RemovedIsStableOrdered pins the
// deterministic-ordering contract: same input produces same
// output regardless of map-iteration randomness. The CLI's
// printAffectedText and the dashboard's ApplyResult banner both
// sort Removed; this test asserts the partition itself emits a
// sorted slice so a refactor that drops the call-site sort
// doesn't break the invariant.
func TestComputeAffectedPartition_RemovedIsStableOrdered(t *testing.T) {
	// Five apps out of scope; expect alphabetical order.
	apps := []state.App{
		{ID: "5", Slug: "zeta", WorkloadName: "zeta", RootDir: "apps", ProjectID: projectID},
		{ID: "1", Slug: "alpha", WorkloadName: "alpha", RootDir: "apps", ProjectID: projectID},
		{ID: "4", Slug: "mu", WorkloadName: "mu", RootDir: "apps", ProjectID: projectID},
		{ID: "2", Slug: "beta", WorkloadName: "beta", RootDir: "apps", ProjectID: projectID},
		{ID: "3", Slug: "delta", WorkloadName: "delta", RootDir: "apps", ProjectID: projectID},
	}
	scanWl := []reposcan.Workload{}

	got := computeAffectedPartition(scanWl, scanWl, apps, nil, projectID)

	want := []string{"alpha", "beta", "delta", "mu", "zeta"}
	if !reflect.DeepEqual(got.Removed, want) {
		t.Fatalf("Removed not stable-ordered:\n  got:  %#v\n  want: %#v", got.Removed, want)
	}

	// Run again with the apps in reverse input order — output
	// must be identical (sort is stable; the input order doesn't
	// leak).
	reverse := make([]state.App, len(apps))
	for i, a := range apps {
		reverse[len(apps)-1-i] = a
	}
	got2 := computeAffectedPartition(scanWl, scanWl, reverse, nil, projectID)
	if !reflect.DeepEqual(got2.Removed, want) {
		t.Fatalf("Removed order depends on input order:\n  got:  %#v\n  want: %#v", got2.Removed, want)
	}
}

// TestComputeAffectedPartition_UnaffectedStaysAccountScoped
// pins the "Unaffected is the blast-radius view" invariant
// (scan_service.go:646-650 doc comment): even with a projectID
// in scope, Unaffected includes apps from other projects. The
// Removed projection is project-scoped; Unaffected is NOT —
// a refactor that swaps the two would silently narrow the
// operator's blast-radius awareness.
func TestComputeAffectedPartition_UnaffectedStaysAccountScoped(t *testing.T) {
	apps := fixtureApps() // includes app-cross in otherProjectID
	scanWl := fixtureScanWl()

	got := computeAffectedPartition(scanWl, scanWl, apps, nil, projectID)

	// Removed is project-scoped: only `orphan` (in projectID).
	if !reflect.DeepEqual(got.Removed, []string{"orphan"}) {
		t.Fatalf("Removed should be project-scoped: got %#v", got.Removed)
	}

	// Unaffected is account-scoped: includes `cross-project`.
	gotUnaffectedSlugs := make([]string, 0, len(got.Unaffected))
	for _, row := range got.Unaffected {
		gotUnaffectedSlugs = append(gotUnaffectedSlugs, row.Slug)
	}
	sort.Strings(gotUnaffectedSlugs)

	wantUnaffected := []string{"cross-project", "orphan"}
	// `matched` is filtered out by the scanKeys check; `orphan`
	// and `cross-project` are the only apps not in scanKeys.
	if !reflect.DeepEqual(gotUnaffectedSlugs, wantUnaffected) {
		t.Fatalf("Unaffected should be account-scoped:\n  got:  %#v\n  want: %#v", gotUnaffectedSlugs, wantUnaffected)
	}
}
