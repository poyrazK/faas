// scan_project_exclude_e2e_test.go — non-metal CI-safe acceptance
// for the ADR-124 `--exclude` end-to-end surface.
//
// What this test pins
//
//   - The end-to-end pipeline that takes a customer tarball at the
//     wire surface and produces a `PlanResponse` whose
//     `will_deploy` / `unaffected` / `skipped` / `removed` partition
//     honors the operator's `--exclude` set:
//
//       1. Brand-new scan workloads listed in `exclude` surface in
//          `Skipped` (Action="noop") and do NOT appear in
//          `WillDeploy`. This is the operator's "I want to skip
//          this for now" intent on the scan path.
//
//       2. Existing apps protected by `exclude` do NOT appear in
//          `Removed` even when the scan omits them. The post-#1065
//          audit fix (`TestReconcile_ExcludePreventsRemove`)
//          guarantees that an operator `--exclude=foo` for an
//          EXISTING app foo does NOT trigger `applyRemove` on the
//          apply path. This test pins the scan-side corollary:
//          the scan response itself must NOT put the excluded
//          slug in `Removed`.
//
//       3. Multi-slug exclude (`exclude=a,b,c`) — every slug
//          surfaces in `Skipped`; no excluded slug appears in
//          `WillDeploy` or as a `create` Action anywhere.
//
//       4. `--only` / `--exclude` mutex is enforced server-side —
//          a request with overlapping slugs returns HTTP 409 with
//          RFC 7807 code `exclude_only_overlap`.
//
//       5. `exclude_unknown_slug` — a slug listed in `exclude` but
//          not present in the scan returns HTTP 400 with RFC 7807
//          code `exclude_unknown_slug`. The apply path also filters
//          excluded workloads out of `filteredW` BEFORE reconcile
//          runs (defence-in-depth), so a typo on `--exclude`
//          cannot accidentally soft-delete an existing app.
//
// What this test deliberately does NOT cover
//
//   - The gate-rescue wire field (`GateRescuedByExclude`,
//     `CanApplyReasons`) — covered by PR-A's unit tests
//     (`pkg/wire/metrics_test.go::TestPlanGateRescuedByExclude_*`)
//     and the
//     `cmd/apid/scan_service_audit_test.go::TestEmitSkippedAuditRows_BrandNewExcluded`
//     unit test. Driving the gate rescue end-to-end here would
//     require seeding a Free-plan account above the apps/cron
//     quota, which is a separate harness path.
//   - The apply path (POST /v1/projects) — covered by
//     `signed_deploy_e2e_test.go` for the trusted-signer gate.
//     The scan surface is the one operators use to verify the
//     partition before confirming.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS or when pgtest.Open returns nil).

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// scanProjectMultipartWithExclude POSTs to /v1/projects/scan with
// the given `source` bytes + form fields. Mirrors the helper at
// scan_project_e2e_test.go:208, plus an `exclude` field for the
// ADR-124 inverse-allowlist. Returns the parsed PlanResponse or
// fails the test.
//
// On non-2xx responses, returns the status code + raw body so the
// caller can assert 4xx codes (e.g. exclude_only_overlap,
// exclude_unknown_slug). The struct is only populated on 200.
func scanProjectMultipartWithExclude(t *testing.T, h *e2etest.Harness,
	key, slug, only, exclude string, body []byte,
) (api.PlanResponse, int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	src, err := mw.CreateFormFile("source", "fixture.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := src.Write(body); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if slug != "" {
		if err := mw.WriteField("project_slug", slug); err != nil {
			t.Fatalf("write project_slug: %v", err)
		}
	}
	if only != "" {
		if err := mw.WriteField("only", only); err != nil {
			t.Fatalf("write only: %v", err)
		}
	}
	if exclude != "" {
		if err := mw.WriteField("exclude", exclude); err != nil {
			t.Fatalf("write exclude: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.APIDURL+"/v1/projects/scan", &buf)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("scan request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return api.PlanResponse{}, resp.StatusCode, string(raw)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode PlanResponse: %v (body=%s)", err, raw)
	}
	return plan, resp.StatusCode, string(raw)
}

// singleWorkloadFixture is defined in apply_project_guards_e2e_test.go
// (one convention-detector workload named `api`). We reuse it here so
// the partition assertions stay small and the convention detector
// walks `services/api/` exactly once.
//
// TestScanExclude_BrandNewExcluded pins the operator's
// "skip this workload for now" intent on the scan path. The
// workload exists in the scan (RootDir="services/api", Name="api"),
// the operator excludes it, and the partition must reflect the
// intent:
//
//   - `skipped` carries the row with Action="noop"
//   - `will_deploy` does NOT carry the row
//   - `can_apply` is true (under-quota on Pro plan)
//
// Without --exclude the same scan produces a `create` row in
// will_deploy (covered indirectly by
// scan_project_e2e_test.go::TestScanProject_MultiTierFixture_UnderQuota
// — the same scan fixture without exclude produces a non-empty
// will_deploy for the multi-tier case).
func TestScanExclude_BrandNewExcluded(t *testing.T) {
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
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	key := h.SeedAccount(context.Background(), api.PlanPro, "exclude-brand-new")

	plan, status, body := scanProjectMultipartWithExclude(t, h, key,
		"exclude-brand-new", "", "api", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}
	if !plan.CanApply {
		t.Errorf("CanApply = false on Pro plan under quota; observed_apps=%d limit_apps=%d",
			plan.ObservedApps, plan.LimitApps)
	}

	// The excluded workload must NOT appear in WillDeploy.
	for _, w := range plan.WillDeploy {
		if w.Slug == "api" {
			t.Errorf("WillDeploy carries excluded slug %q; want absent", w.Slug)
		}
	}

	// The excluded workload MUST appear in Skipped with Action="noop".
	var sawAPI bool
	for _, s := range plan.Skipped {
		if s.Slug == "api" {
			sawAPI = true
			if s.Action != "noop" {
				t.Errorf("Skipped[api].Action = %q, want noop", s.Action)
			}
		}
	}
	if !sawAPI {
		t.Errorf("Skipped partition missing excluded slug 'api'; got: %+v", plan.Skipped)
	}
}

// TestScanExclude_ExistingAppRescued pins that an operator
// `--exclude=existing-app` for an EXISTING app does NOT put the
// app in `removed`. The audit fix at
// pkg/reconcile/reconcile_test.go::TestReconcile_ExcludePreventsRemove
// pins the apply-side contract; this test pins the scan-side
// corollary.
//
// Setup: pre-seed an app `legacy-api` with a (RootDir, Name)
// that the scan will NOT discover (RootDir="external/legacy",
// Name="legacy-api"). Without exclude, the scan would put
// `legacy-api` in `removed` (existing app, no scan workload).
// With exclude=legacy-api, the scan must keep `removed` empty —
// the operator's intent is "this is long-term excluded; do not
// touch on this deploy."
func TestScanExclude_ExistingAppRescued(t *testing.T) {
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
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()

	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "e2e+exclude-existing@test.example",
		Plan:  api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, res.Account.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	// Seed the legacy app BEFORE the scan runs. The Removed
	// partition is project-scoped (multi-project safety —
	// pkg/reconcile/partition comment); we MUST create the
	// project row + assign the legacy app to it so the
	// baseline (no exclude) scan surfaces `legacy-api` in
	// Removed. Without the project_id binding, the partition
	// leaves Removed empty by design and the baseline assertion
	// at line 276 trips.
	//
	// The partition's Removed exclude-filter checks BOTH
	// `a.Slug` and `a.WorkloadName` (the dual-key
	// `exclude[strings.ToLower(a.Slug)] ||
	// exclude[strings.ToLower(a.WorkloadName)]` check
	// added in this same fix), so an app with empty
	// WorkloadName is still rescued by `--exclude=<slug>`.
	proj, err := store.CreateProject(ctx, state.Project{
		AccountID: res.Account.ID,
		Slug:      "exclude-existing",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	legacy, err := store.CreateApp(ctx, state.App{
		AccountID:      res.Account.ID,
		Slug:           "legacy-api",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		ProjectID:      proj.ID,
	})
	if err != nil {
		t.Fatalf("CreateApp legacy: %v", err)
	}

	// Sanity: without exclude, the scan MUST put legacy-api in Removed.
	planNoExclude, status, body := scanProjectMultipartWithExclude(t, h, pt,
		"exclude-existing", "", "", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}
	var sawRemoved bool
	for _, slug := range planNoExclude.Removed {
		if slug == legacy.Slug {
			sawRemoved = true
		}
	}
	if !sawRemoved {
		t.Errorf("baseline: Removed missing legacy-api without exclude; got: %+v",
			planNoExclude.Removed)
	}

	// With exclude=legacy-api, the operator's intent is honoured:
	// the scan must NOT put legacy-api in Removed. The scan-side
	// contract mirrors the apply-side contract pinned at
	// TestReconcile_ExcludePreventsRemove.
	planWithExclude, status, body := scanProjectMultipartWithExclude(t, h, pt,
		"exclude-existing", "", "legacy-api", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}
	for _, slug := range planWithExclude.Removed {
		if slug == legacy.Slug {
			t.Errorf("Removed carries excluded slug %q; --exclude must rescue "+
				"existing apps from the removed partition", slug)
		}
	}
}

// TestScanExclude_MultiSlug pins that a comma-separated
// `exclude=a,b,c` surfaces every slug in `Skipped` and that no
// excluded slug appears in `WillDeploy`. The fixture is the
// multi-tier plan from scan_project_e2e_test.go; we exclude the
// three "service" workloads (api, worker, web) and assert the
// partition honours the multi-slug shape.
func TestScanExclude_MultiSlug(t *testing.T) {
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
	key := h.SeedAccount(context.Background(), api.PlanPro, "exclude-multi-slug")

	plan, status, body := scanProjectMultipartWithExclude(t, h, key,
		"exclude-multi-slug", "", "api,web,worker", scanProjectFixture(t))
	if status != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", status, body)
	}

	want := map[string]bool{"api": true, "web": true, "worker": true}
	got := make(map[string]bool, len(plan.Skipped))
	for _, s := range plan.Skipped {
		if want[s.Slug] {
			got[s.Slug] = true
		}
		if s.Action != "noop" {
			t.Errorf("Skipped[%s].Action = %q, want noop", s.Slug, s.Action)
		}
	}
	for slug := range want {
		if !got[slug] {
			t.Errorf("Skipped partition missing %q; got: %+v", slug, plan.Skipped)
		}
	}

	// None of the excluded slugs may appear in WillDeploy.
	for _, w := range plan.WillDeploy {
		if want[w.Slug] {
			t.Errorf("WillDeploy carries excluded slug %q; want absent", w.Slug)
		}
	}
}

// TestScanExclude_OnlyMutexRejected pins the server-side
// `--only` / `--exclude` overlap rejection. Per ADR-124 §1 the
// operator contract is "you cannot use both at once"; the server
// returns HTTP 409 with RFC 7807 code `exclude_only_overlap`
// before the scan engine runs (defence-in-depth — applying an
// overlapping pair would silently flip the trust model).
func TestScanExclude_OnlyMutexRejected(t *testing.T) {
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
	key := h.SeedAccount(context.Background(), api.PlanPro, "exclude-mutex")

	status, body := func() (int, string) {
		_, s, b := scanProjectMultipartWithExclude(t, h, key,
			"exclude-mutex", "api", "api", singleWorkloadFixture(t))
		return s, b
	}()
	if status != http.StatusConflict {
		t.Fatalf("overlapping only+exclude status = %d, want 409 (body=%s)", status, body)
	}
	// The body MUST carry the RFC 7807 code "exclude_only_overlap".
	if !strings.Contains(body, "exclude_only_overlap") {
		t.Errorf("overlap response missing code=exclude_only_overlap; body=%s", body)
	}
}

// TestScanExclude_UnknownSlugRejected pins that a slug listed in
// `exclude` but not present in the scan returns HTTP 400 with
// RFC 7807 code `exclude_unknown_slug`. Per ADR-124 §4, an
// unknown slug is a programming-error surface — the operator can
// fix it by removing the typo. Silent ignore would be worse: a
// typo on `--exclude=payments-apii` would silently apply the
// deploy without the exclusion.
func TestScanExclude_UnknownSlugRejected(t *testing.T) {
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
	key := h.SeedAccount(context.Background(), api.PlanPro, "exclude-unknown")

	status, body := func() (int, string) {
		_, s, b := scanProjectMultipartWithExclude(t, h, key,
			"exclude-unknown", "", "does-not-exist", singleWorkloadFixture(t))
		return s, b
	}()
	if status != http.StatusBadRequest {
		t.Fatalf("unknown-slug exclude status = %d, want 400 (body=%s)", status, body)
	}
	if !strings.Contains(body, "exclude_unknown_slug") {
		t.Errorf("unknown-slug response missing code=exclude_unknown_slug; body=%s", body)
	}
}
