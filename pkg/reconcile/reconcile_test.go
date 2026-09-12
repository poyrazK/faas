// Unit tests for pkg/reconcile. Table-driven where the input
// shape is stable; individual TestXxx functions where the audit
// chronology matters more than the input shape. The fakeStore
// (fakes_test.go) is the substrate; the fakeAuditor is a real
// pkg/audit.Auditor backed by the fakeStore so AppendEvent
// calls land in the recorded-event slice.
//
// Each test:
//   1. Seeds an Account + Project via the fakeStore's MemStore
//      (using the project's ProductionBranch + ScanSource as the
//      test case distinguishes).
//   2. Builds a reposcan.Result (no fs.FS — the diff is the unit
//      under test, not the parsers).
//   3. Calls Service.Reconcile.
//   4. Asserts on Result + the recorded event slice.

package reconcile

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ---------- helpers ----------

// seedProject inserts an account + a project with the given
// productionBranch + scanSource. Returns the project.
func seedProject(t *testing.T, store *fakeStore, scanSource state.ProjectScanSource, prodBranch string) (state.Account, state.Project) {
	t.Helper()
	acct := store.putAccount("acct-1")
	proj, err := store.CreateProject(context.Background(), state.Project{
		AccountID:        acct.ID,
		Slug:             "demo",
		RepoFullName:     "octocat/demo",
		ProductionBranch: prodBranch,
		ScanSource:       scanSource,
	})
	if err != nil {
		t.Fatalf("seedProject: %v", err)
	}
	return acct, proj
}

// seedApp inserts an app with the given (rootDir, workloadName)
// pair into the project via MemStore.CreateApp. The store stamps
// id, created_at, and status='active'. The test reads the App
// back from AppsForProject so the WorkloadName-based merge key
// captures the canonical values.
//
// Slug mirrors the production path: verbatim w.Name, no slugify.
// apps.slug is unconstrained in the schema; reconcile + apid
// must agree (see workloadToDraftApp's docstring for the
// rationale + the dual-pin requirement).
func seedApp(t *testing.T, store *fakeStore, project state.Project, rootDir, workloadName, startCmd string) state.App {
	t.Helper()
	a := state.App{
		AccountID:     project.AccountID,
		ProjectID:     project.ID,
		Slug:          workloadName,
		RootDir:       rootDir,
		WorkloadName:  workloadName,
		WorkloadClass: state.WorkloadClassHTTP,
		StartCommand:  startCmd,
		Status:        state.AppActive,
	}
	added, err := store.CreateApp(context.Background(), a)
	if err != nil {
		t.Fatalf("seedApp: %v", err)
	}
	return added
}

// appScanResult builds a 3-workload Result sorted by name.
func threeWorkloads(t *testing.T, rootDir string) reposcan.Result {
	t.Helper()
	return reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: rootDir, Class: reposcan.ClassHTTP, Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "worker", RootDir: rootDir, Class: reposcan.ClassWorker, Source: "compose.yaml: worker", Tier: reposcan.TierCompose},
			{Name: "web", RootDir: rootDir, Class: reposcan.ClassHTTP, Source: "compose.yaml: web", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
}

// extractAppIDs returns the IDs from a Result.Added / Changed slice.
func extractAppIDs(apps []state.App) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.ID)
	}
	sort.Strings(out)
	return out
}

// extractKinds returns the sorted kind list from the recorded
// audit events. The unit tests don't pin the per-event payload
// shape (that's covered by the audit-row content tests); the
// order + kinds are the load-bearing assertions.
func extractKinds(events []fakeEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

// equalSlices returns true iff a and b have the same length and
// the same elements at every index. Used by audit-ordering
// assertions where extractKinds's "sorted" return would mask the
// very chronology the test is trying to pin.
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findEvent returns the first recorded event whose kind matches.
// Returns ok=false if no event matches.
func findEvent(events []fakeEvent, kind string) (fakeEvent, bool) {
	for _, e := range events {
		if e.Kind == kind {
			return e, true
		}
	}
	return fakeEvent{}, false
}

// freshService wires a fakeStore + fakeAuditor into a Service.
// scan is the reposcan entry used by the test (default: nil →
// the real reposcan.Scan is never called because the unit tests
// pass the scan Result directly).
func freshService(store *fakeStore, aud *audit.Auditor) *Service {
	s := NewService(store, aud, nil)
	// The unit tests pass scan Result directly; the Scan func on
	// Service is unused. Keep the default for safety.
	return s
}

// ---------- audit helper re-import for the test signature ----------
// (audit.Auditor is used in the helper signatures above; the
//  import resolves through the import block below.)

// ---------- tests ----------

func TestReconcile_ThreeWorkloads_NoDiff(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")
	seedApp(t, store, proj, "", "worker", "")
	seedApp(t, store, proj, "", "web", "")

	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, threeWorkloads(t, ""), "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 0 || len(out.Changed) != 0 || len(out.Removed) != 0 {
		t.Errorf("expected zero actions, got added=%d changed=%d removed=%d",
			len(out.Added), len(out.Changed), len(out.Removed))
	}
	if out.WasIgnored {
		t.Errorf("WasIgnored should be false")
	}
	// Only the started audit row should fire on a no-op pass.
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 1 || kinds[0] != KindReconcileStarted {
		t.Errorf("expected 1 event (started), got %v", kinds)
	}
}

func TestReconcile_ThreeWorkloads_AddOne(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")
	seedApp(t, store, proj, "", "worker", "")

	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, threeWorkloads(t, ""), "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 1 {
		t.Fatalf("expected 1 create, got %d", len(out.Added))
	}
	if out.Added[0].WorkloadName != "web" {
		t.Errorf("expected create=workload_name=web, got %q", out.Added[0].WorkloadName)
	}
	// Audit: started + 1 added.
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 2 || kinds[0] != KindReconcileStarted || kinds[1] != KindWorkloadAdded {
		t.Errorf("expected [started, added], got %v", kinds)
	}
}

func TestReconcile_ThreeWorkloads_RemoveOne(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")
	seedApp(t, store, proj, "", "worker", "")
	seedApp(t, store, proj, "", "extrasvc", "")

	// Only api + worker survive.
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "worker", RootDir: "", Source: "compose.yaml: worker", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Removed) != 1 {
		t.Fatalf("expected 1 remove, got %d", len(out.Removed))
	}
	// Audit: started + 1 removed (the audit row fires BEFORE the
	// cascade, so the chronology is [started, removed]).
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 2 || kinds[0] != KindReconcileStarted || kinds[1] != KindWorkloadRemoved {
		t.Errorf("expected [started, removed], got %v", kinds)
	}
}

// TestReconcile_ExcludePreventsRemove pins code-review finding
// #1 (PR #1065): the operator --exclude set MUST be honored by
// workloadDiff. The pre-fix implementation filtered the SCAN
// side (filteredW drops workloads whose name is in exclude)
// but did NOT filter the EXISTING side, so a scan that drops
// `extrasvc` (excluded) while leaving `api` + `worker` would
// still emit a remove Action for `extrasvc` →
// applyActions.applyRemove → SoftDeleteAppCascade. The
// operator's `--exclude=extrasvc` was silently overridden.
//
// The fix: workloadDiff now filters `existing` by excludeSet
// before the removes loop. This test seeds three apps
// (api, worker, extrasvc), scans with only api + worker, and
// passes exclude=["extrasvc"]. Expected:
//
//  1. out.Removed is empty (no soft-delete).
//  2. extrasvc is still present in the store (not deleted).
//  3. No KindWorkloadRemoved audit row fired.
//  4. The audit chain is [started] + maybe KindWorkloadAdded/Changed
//     but NO removed row.
func TestReconcile_ExcludePreventsRemove(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")
	seedApp(t, store, proj, "", "worker", "")
	seedApp(t, store, proj, "", "extrasvc", "")

	// Scan emits only api + worker (extrasvc is "removed" from
	// the scan side). The exclude list mirrors what an operator
	// would type: `--exclude=extrasvc` meaning "don't touch the
	// extrasvc app at all".
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "worker", RootDir: "", Source: "compose.yaml: worker", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", []string{"extrasvc"})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Removed) != 0 {
		t.Fatalf("exclude=extrasvc must prevent remove; got Removed=%v", out.Removed)
	}

	// extrasvc must still be present (not soft-deleted).
	live, lerr := store.AppsForProject(context.Background(), proj.AccountID, proj.ID)
	if lerr != nil {
		t.Fatalf("AppsForProject: %v", lerr)
	}
	foundExtrasvc := false
	for _, a := range live {
		if a.WorkloadName == "extrasvc" {
			foundExtrasvc = true
			break
		}
	}
	if !foundExtrasvc {
		t.Errorf("extrasvc must remain live (exclude set prevents remove); live=%v", live)
	}

	// Audit: started only — NO KindWorkloadRemoved.
	kinds := extractKinds(store.snapshotEvents())
	for _, k := range kinds {
		if k == KindWorkloadRemoved {
			t.Errorf("audit must NOT include KindWorkloadRemoved when exclude=[extrasvc]; got kinds=%v", kinds)
		}
	}
	if len(kinds) == 0 || kinds[0] != KindReconcileStarted {
		t.Errorf("expected audit to begin with %q; got %v", KindReconcileStarted, kinds)
	}
}

// TestReconcile_ExcludeMixedCaseAndTrim pins the
// normalization contract: workloadDiff's exclude filter is
// case-insensitive on WorkloadName AND trims surrounding
// whitespace from operator input. The handler converts via
// `strings.ToLower(strings.TrimSpace(name))` before threading
// through; a future refactor that drops the trim would let
// " extrasvc " (with surrounding spaces from copy-paste)
// leak past the filter and soft-delete the app.
func TestReconcile_ExcludeMixedCaseAndTrim(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "ExtraSvc", "") // mixed-case name on the store side

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{},
		Tier:      reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	// Pass with mixed-case + whitespace; the filter MUST match
	// "extrasvc" (lowercased) against "ExtraSvc" (lowercased).
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", []string{" EXTRASVC "})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Removed) != 0 {
		t.Errorf("mixed-case+trimmed exclude must prevent remove; got Removed=%v", out.Removed)
	}
}

func TestReconcile_ThreeWorkloads_ChangeRootDir(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	// Existing app has rootDir="apps/api" and the scan also has
	// rootDir="apps/api" but start_command differs. The diff key
	// (rootDir, name) collides, so the diff produces an update
	// (not a remove+create).
	seedApp(t, store, proj, "apps/api", "api", "")
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "apps/api", Command: []string{"python", "app.py"}, Source: "compose.yaml: api", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Changed) != 1 {
		t.Fatalf("expected 1 update, got %d", len(out.Changed))
	}
	if out.Changed[0].StartCommand != "python app.py" {
		t.Errorf("expected start_command=python app.py, got %q", out.Changed[0].StartCommand)
	}
	// Audit row payload should include "start_command" in fields_changed.
	ev, ok := findEvent(store.snapshotEvents(), KindWorkloadChanged)
	if !ok {
		t.Fatalf("missing workload.changed audit row")
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("audit payload unparseable: %v", err)
	}
	fields, ok := data["fields_changed"].([]any)
	if !ok {
		t.Fatalf("fields_changed missing or wrong type: %v", data["fields_changed"])
	}
	if len(fields) != 1 || fields[0] != "start_command" {
		t.Errorf("expected fields_changed=[start_command], got %v", fields)
	}
}

func TestReconcile_ScanSourceDowngrade(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	// Project's stored scan_source is Compose; the scan produces
	// only Convention-level seeds (no compose file). The guard
	// computes "convention" and compares against Compose via
	// tierRank. Compose(8) > Convention(2) → downgrade.
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "apps/api", Source: "convention: apps/api", Tier: reposcan.TierConvention},
		},
		Tier: reposcan.TierConvention,
	}
	svc := freshService(store, aud)
	_, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err == nil {
		t.Fatalf("expected downgrade rejection, got nil")
	}
	if !strings.Contains(err.Error(), "scan_source") {
		t.Errorf("expected scan_source-related error, got %v", err)
	}
	// Audit: alert only.
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 1 || kinds[0] != KindReconcileAlert {
		t.Errorf("expected [alert], got %v", kinds)
	}
}

func TestReconcile_FeatureBranch_NoDiff(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")

	svc := freshService(store, aud)
	// branch="feature/x" != project.ProductionBranch="main".
	out, err := svc.Reconcile(context.Background(), proj, threeWorkloads(t, ""), "sha-1", "feature/x", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !out.WasIgnored {
		t.Errorf("WasIgnored should be true")
	}
	if len(out.Added) != 0 || len(out.Changed) != 0 || len(out.Removed) != 0 {
		t.Errorf("expected zero actions, got added=%d changed=%d removed=%d",
			len(out.Added), len(out.Changed), len(out.Removed))
	}
	// Audit: alert only.
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 1 || kinds[0] != KindReconcileAlert {
		t.Errorf("expected [alert], got %v", kinds)
	}
}

func TestReconcile_ZeroWorkloads_AlertEmitted(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")

	svc := freshService(store, aud)
	scan := reposcan.Result{Workloads: nil, Tier: 0}
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindNoWorkloads {
		t.Errorf("expected no_workloads alert, got %v", out.Alerts)
	}
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 1 || kinds[0] != KindReconcileAlert {
		t.Errorf("expected [alert], got %v", kinds)
	}
}

func TestReconcile_AuditOrdering_RemovedBeforeCascade(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")
	seedApp(t, store, proj, "", "toRemove", "")

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	_, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Chronology: started, removed. The cascade SQL fires AFTER
	// the audit row.
	events := store.snapshotEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != KindReconcileStarted {
		t.Errorf("events[0] should be started, got %s", events[0].Kind)
	}
	if events[1].Kind != KindWorkloadRemoved {
		t.Errorf("events[1] should be removed, got %s", events[1].Kind)
	}
}

func TestReconcile_OverQuota_CreatesSkipped(t *testing.T) {
	store := newFakeStore()
	// Free plan has deployed_apps cap = 1.
	store.accountPlan = api.PlanFree
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "api", "")

	// Attempt 3 creates. Free cap = 1. proj = 1 existing + 3 = 4.
	// projected > cap → rejected before any mutation.
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, threeWorkloads(t, ""), "sha-1", "main", nil)
	if err == nil {
		t.Fatal("expected quota error")
	}
	if len(out.Added) != 0 {
		t.Errorf("expected zero creates on over-quota, got %d", len(out.Added))
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindQuotaBlocked {
		t.Errorf("expected quota_blocked alert, got %v", out.Alerts)
	}
	// Audit: started + quota_blocked.
	kinds := extractKinds(store.snapshotEvents())
	if len(kinds) != 2 || kinds[0] != KindReconcileStarted || kinds[1] != KindReconcileQuotaBlocked {
		t.Errorf("expected [started, quota_blocked], got %v", kinds)
	}
}

func TestReconcile_AtomicQuotaErrorRollsBack(t *testing.T) {
	// The transactional project path must surface a quota race as an error
	// and leave the existing membership untouched. The hook stands in for a
	// PostgreSQL transaction that rejects the authoritative quota check.
	store := newFakeStore()
	store.accountPlan = api.PlanHobby
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	seedApp(t, store, proj, "", "existing", "")

	store.applyProjectReconcileHook = func(_ state.Project, _ []state.ProjectReconcileMutation, _ []state.ProjectReconcileCron, _ state.ProjectScanSource, _ api.Limits) (state.ProjectReconcileResult, error) {
		return state.ProjectReconcileResult{}, &state.QuotaError{
			Kind:     state.QuotaErrorKindApps,
			Limit:    5,
			Observed: 6,
		}
	}

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "existing", RootDir: "", Source: "compose.yaml: existing", Tier: reposcan.TierCompose},
			{Name: "new-a", RootDir: "", Source: "compose.yaml: new-a", Tier: reposcan.TierCompose},
			{Name: "new-b", RootDir: "", Source: "compose.yaml: new-b", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-inner-q", "main", nil)
	if err == nil {
		t.Fatal("expected quota error")
	}
	if len(out.Added) != 0 {
		t.Fatalf("expected no committed adds, got %d", len(out.Added))
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindQuotaBlocked {
		t.Fatalf("expected 1 quota_blocked alert, got %v", out.Alerts)
	}
	apps, listErr := store.AppsForProject(context.Background(), proj.AccountID, proj.ID)
	if listErr != nil {
		t.Fatalf("AppsForProject: %v", listErr)
	}
	if len(apps) != 1 || apps[0].WorkloadName != "existing" {
		t.Fatalf("partial app mutation leaked after rollback: %#v", apps)
	}
	// Audit row payload is the source of truth for dashboards;
	// pin it too.
	ev, ok := findEvent(store.snapshotEvents(), KindReconcileQuotaBlocked)
	if !ok {
		t.Fatalf("missing quota_blocked audit row")
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("audit payload unparseable: %v", err)
	}
	auditSkipped, _ := data["skipped_creates"].([]any)
	if len(auditSkipped) != 2 {
		t.Errorf("audit row skipped_creates: expected 2, got %d (%v)", len(auditSkipped), auditSkipped)
	}
	// Chronology: started, quota_blocked; no workload row may claim a commit.
	kinds := extractKinds(store.snapshotEvents())
	want := []string{KindReconcileStarted, KindReconcileQuotaBlocked}
	if !equalSlices(kinds, want) {
		t.Errorf("expected kinds %v, got %v", want, kinds)
	}
}

func TestReconcileWithCrons_ReplacesIdempotently(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	app := seedApp(t, store, proj, "", "api", "")
	if _, err := store.CreateCron(context.Background(), app.ID, "*/5 * * * *", "/", true); err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	svc := freshService(store, aud)
	scan := reposcan.Result{Workloads: []reposcan.Workload{{Name: "api", Tier: reposcan.TierCompose, Source: "compose.yaml: api", Schedule: "0 * * * *"}}, Tier: reposcan.TierCompose}
	cron := []CronSpec{{WorkloadName: "api", Schedule: "0 * * * *", Path: "/", Enabled: true}}
	if _, err := svc.ReconcileWithCrons(context.Background(), proj, scan, "sha-cron-1", "main", nil, cron); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if _, err := svc.ReconcileWithCrons(context.Background(), proj, scan, "sha-cron-2", "main", nil, cron); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if _, err := svc.Reconcile(context.Background(), proj, scan, "sha-cron-legacy", "main", nil); err != nil {
		t.Fatalf("legacy reconcile: %v", err)
	}
	crons, err := store.ListCronsForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListCronsForApp: %v", err)
	}
	if len(crons) != 1 || crons[0].Schedule != "0 * * * *" {
		t.Fatalf("expected one replaced cron, got %#v", crons)
	}
	if _, err := svc.ReconcileWithCrons(context.Background(), proj, reposcan.Result{Workloads: []reposcan.Workload{{Name: "api", Tier: reposcan.TierCompose, Source: "compose.yaml: api"}}, Tier: reposcan.TierCompose}, "sha-cron-3", "main", nil, []CronSpec{}); err != nil {
		t.Fatalf("cron removal reconcile: %v", err)
	}
	crons, err = store.ListCronsForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListCronsForApp after removal: %v", err)
	}
	if len(crons) != 0 {
		t.Fatalf("expected cron set to be empty, got %#v", crons)
	}
}

func TestReconcile_DeriveScanSource_MirrorsApid(t *testing.T) {
	// Pin the priority list. If the cmd/apid list ever changes,
	// this test breaks and the reviewer has to update both
	// copies. The failure mode is intentional: silent divergence
	// is worse than a noisy merge conflict.
	cases := []struct {
		name      string
		workloads []reposcan.Workload
		want      state.ProjectScanSource
	}{
		{
			name: "compose wins over convention",
			workloads: []reposcan.Workload{
				{Name: "web", Source: "convention: apps/web"},
				{Name: "api", Source: "compose.yaml: api"},
			},
			want: state.ProjectScanSourceCompose,
		},
		{
			name: "compose wins over k8s when both present (priority list)",
			workloads: []reposcan.Workload{
				{Name: "api", Source: "compose.yaml: api"},
				{Name: "web", Source: "k8s/deployment.yaml: web"},
			},
			want: state.ProjectScanSourceCompose,
		},
		{
			// Repro for the round-3 CI failure: the compose
			// detector emits the actual filename in the source
			// (e.g. "docker-compose.yml: api"), not a literal
			// "compose:" prefix. The priority list must accept
			// every compose-family filename or scan_source
			// falls through to "single" / "unknown" and the
			// monotonic-upgrade guard rejects the re-apply.
			name: "docker-compose.yml filename is recognised as compose",
			workloads: []reposcan.Workload{
				{Name: "api", Source: "docker-compose.yml: api"},
			},
			want: state.ProjectScanSourceCompose,
		},
		{
			name: "compose.yml filename is recognised as compose",
			workloads: []reposcan.Workload{
				{Name: "api", Source: "compose.yml: api"},
			},
			want: state.ProjectScanSourceCompose,
		},
		{
			name: "single workload with root-floor source",
			workloads: []reposcan.Workload{
				{Name: "app", Source: "root-floor"},
			},
			want: state.ProjectScanSourceSingle,
		},
		{
			name:      "empty workload list",
			workloads: nil,
			want:      state.ProjectScanSourceUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveScanSource(tc.workloads)
			if got != tc.want {
				t.Errorf("DeriveScanSource(%v) = %q, want %q", tc.workloads, got, tc.want)
			}
		})
	}
}

func TestReconcile_StartCommand_Flattened(t *testing.T) {
	// resolveStartCommand joins []string with " " — pinned by the
	// unit test so a future refactor doesn't change the wire
	// shape.
	cases := []struct {
		w    reposcan.Workload
		want string
	}{
		{reposcan.Workload{Command: []string{"python", "app.py"}}, "python app.py"},
		{reposcan.Workload{Command: nil}, ""},
		{reposcan.Workload{Command: []string{}}, ""},
	}
	for _, tc := range cases {
		got := resolveStartCommand(tc.w)
		if got != tc.want {
			t.Errorf("resolveStartCommand(%v) = %q, want %q", tc.w, got, tc.want)
		}
	}
}

func TestReconcile_AppliedIDs_IsolatesAddsFromChanged(t *testing.T) {
	// Regression for #428-style drift: a "changed" must NEVER
	// also appear in "added". The diff's creates and updates are
	// disjoint by construction; this test pins the invariant.
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	// Seed the existing api app at the SAME rootDir as the scan
	// so the diff key (rootDir, name) collides → update path.
	seedApp(t, store, proj, "apps/api", "api", "")

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "apps/api", Command: []string{"python", "app.py"}, Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "worker", RootDir: "apps/worker", Source: "compose.yaml: worker", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	added := extractAppIDs(out.Added)
	changed := extractAppIDs(out.Changed)
	if len(added) != 1 {
		t.Errorf("expected 1 add, got %d", len(added))
	}
	if len(changed) != 1 {
		t.Errorf("expected 1 change, got %d", len(changed))
	}
	for _, c := range changed {
		for _, a := range added {
			if c == a {
				t.Errorf("changed %s also in added", c)
			}
		}
	}
}

// TestPlan_NoMutation_ProjectsDiff pins the Plan path's central
// invariant: it returns Added/Changed/Removed projections identical
// to what Reconcile would emit, but the fakeStore's existing apps
// remain untouched and the event slice stays empty (no audit rows).
// apid's dry-run endpoint relies on this — calling Plan must never
// side-effect the database.
func TestPlan_NoMutation_ProjectsDiff(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newFakeAuditor(store), nil)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "web", RootDir: "", Source: "compose.yaml: web", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	out, err := svc.Plan(context.Background(), proj, scan, "sha-plan-1", "main", nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(out.Added) != 2 {
		t.Errorf("expected 2 Added in projection, got %d", len(out.Added))
	}
	if len(out.Changed) != 0 {
		t.Errorf("expected 0 Changed, got %d", len(out.Changed))
	}
	if len(out.Removed) != 0 {
		t.Errorf("expected 0 Removed, got %d", len(out.Removed))
	}
	// fakeStore.appendEvents stays empty — Plan never calls Emit.
	if got := len(store.snapshotEvents()); got != 0 {
		t.Errorf("Plan leaked %d audit rows; expected 0", got)
	}
	// Existing project membership unchanged (AppsForProject returns []).
	got, err := store.AppsForProject(context.Background(), proj.AccountID, proj.ID)
	if err != nil {
		t.Fatalf("AppsForProject after Plan: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Plan leaked %d app rows; expected 0", len(got))
	}
}

// TestPlan_FeatureBranchIgnored asserts the production-branch guard
// is honored on the Plan path when branch != "" (apid's dry-run
// can render projections for any branch, but a non-prod branch
// must surface WasIgnored=true so the handler returns
// {ignored: true}).
func TestPlan_FeatureBranchIgnored(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newFakeAuditor(store), nil)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	out, err := svc.Plan(context.Background(), proj, scan, "sha-plan-2", "feature/foo", nil)
	if err != nil {
		t.Fatalf("Plan feature-branch: %v", err)
	}
	if !out.WasIgnored {
		t.Errorf("expected WasIgnored=true on feature branch")
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindFeatureBranch {
		t.Errorf("expected feature_branch alert, got %+v", out.Alerts)
	}
}

// TestPlan_EmptyScan_AlertNoWorkloads pins guard 1 on the Plan path.
func TestPlan_EmptyScan_AlertNoWorkloads(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newFakeAuditor(store), nil)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")

	out, err := svc.Plan(context.Background(), proj, reposcan.Result{Tier: reposcan.TierCompose}, "sha-plan-3", "main", nil)
	if err == nil {
		t.Fatalf("Plan on empty scan must error")
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindNoWorkloads {
		t.Errorf("expected no_workloads alert, got %+v", out.Alerts)
	}
}

// TestPlan_ScanSourceDowngrade_NoMutation pins guard 3 on the Plan
// path: a downgrade is reported but never applied.
func TestPlan_ScanSourceDowngrade_NoMutation(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, newFakeAuditor(store), nil)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "app", Source: "root-floor", Tier: reposcan.TierSingle},
		},
		Tier: reposcan.TierSingle,
	}
	out, err := svc.Plan(context.Background(), proj, scan, "sha-plan-4", "main", nil)
	if err == nil {
		t.Fatalf("Plan on downgrade must error")
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != AlertKindScanSourceDowngrade {
		t.Errorf("expected scan_source_downgrade alert, got %+v", out.Alerts)
	}
	if len(store.snapshotEvents()) != 0 {
		t.Errorf("Plan on downgrade leaked audit rows")
	}
}
