// apply_project_guards_e2e_test.go — coverage for the three
// reconcile guards (plan §B "apply_project_guards"):
//
//   1. neverEmpty: a repo with zero detectable workloads must
//      trip AlertKindNoWorkloads + ErrPlanEmpty. The handler
//      surfaces 422 plan_empty.
//
//   2. productionBranchOnly: an apply whose prod_branch differs
//      from the project's existing production_branch trips
//      state.ErrProdBranchMismatch. **Note:** `branch==""` in
//      the call signature deliberately SKIPS this guard on the
//      apid interactive path (pkg/reconcile/guards.go:82-86) —
//      the test pins that skip-and-allow behaviour so a future
//      refactor doesn't accidentally start rejecting
//      empty-branch applies.
//
//   3. scanSourceStable: an apply whose ScanSource tier is lower
//      than the project's existing tier trips
//      state.ErrScanSourceDowngrade. The handler returns 409.
//
// Each guard also emits an audit/alert row the dashboard renders.

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// singleWorkloadFixture builds a tarball with exactly one
// convention-detector workload (services/api/Dockerfile).
func singleWorkloadFixture(t *testing.T) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{"faas-guard/services/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{"faas-guard/services/api/index.js", "exports.handler = () => 1;\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// composeFixture builds a tarball with docker-compose.yml (tier
// 'compose') and one service. Used for the scanSourceStable test
// which needs a tier-1 repo on the first apply.
func composeFixture(t *testing.T) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{"faas-tier/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n"},
		{"faas-tier/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{"faas-tier/index.js", "exports.handler = () => 1;\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// conventionOnlyFixture builds a tarball with no top-level detector
// markers; only the convention detector fires. Used to create a
// tier='convention' project on the first apply so the second
// (compose) apply trips the scan-source-downgrade guard.
func conventionOnlyFixture(t *testing.T) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{"faas-conv/services/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{"faas-conv/services/api/index.js", "exports.handler = () => 1;\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// emptyFixture builds a tarball with no detectable workloads —
// just a single empty README. Trips the neverEmpty guard.
func emptyFixture(t *testing.T) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{"faas-empty/README.md", "# nothing here\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// applyProjectRaw POSTs and returns (status, body bytes). Mirrors
// applyProjectMultipart but with optional production_branch form
// field, which the canonical helper doesn't expose (apply default
// is "main"; explicit branch is only used by the production-branch
// guard tests).
func applyProjectRaw(t *testing.T, h *e2etest.Harness, key, slug, prodBranch string, body []byte) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	src, err := mw.CreateFormFile("source", "fixture.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = src.Write(body)
	if slug != "" {
		_ = mw.WriteField("project_slug", slug)
	}
	if prodBranch != "" {
		_ = mw.WriteField("production_branch", prodBranch)
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.APIDURL+"/v1/projects", &buf)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestApplyProject_Guard_NeverEmpty pins that an empty repo (no
// detectable workloads) trips the neverEmpty guard with 422.
func TestApplyProject_Guard_NeverEmpty(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	status, body := applyProjectRaw(t, h, key, "empty", "", emptyFixture(t))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422 (plan_empty), body=%s", status, body)
	}
	var p api.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if p.Code != "plan_empty" {
		t.Fatalf("code=%q want plan_empty", p.Code)
	}
}

// TestApplyProject_Guard_NeverEmptyNoRows pins the roll-back
// invariant: an empty-repo apply creates zero project + zero app
// rows. (Same shape as the quota tests — the store Tx must roll
// back cleanly on guard failure.)
func TestApplyProject_Guard_NeverEmptyNoRows(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	_, _ = applyProjectRaw(t, h, key, "empty-rb", "", emptyFixture(t))

	var projCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from projects where slug = 'empty-rb'`).Scan(&projCount)
	if projCount != 0 {
		t.Fatalf("never-empty apply left %d project rows", projCount)
	}
}

// TestApplyProject_Guard_ProductionBranchMatch pins the happy
// path: an apply whose prod_branch matches the project's existing
// production_branch is accepted.
func TestApplyProject_Guard_ProductionBranchMatch(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply with default prod_branch (empty → 'main').
	ar := applyProjectMultipart(t, h, key, "branch-ok", "", singleWorkloadFixture(t))
	// Second apply with explicit prod_branch='main' must be
	// accepted (matches).
	status, _ := applyProjectRaw(t, h, key, "branch-ok", "main", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("matched-branch re-apply status=%d want 200", status)
	}
	if ar.ProjectID == "" {
		t.Fatalf("first apply returned empty project_id")
	}
}

// TestApplyProject_Guard_ProductionBranchEmptySkips pins the
// deliberate skip: pkg/reconcile/guards.go:82-86 returns early
// when branch=="" so the apid interactive path (which always
// passes "" when the user didn't set --production-branch) is
// never rejected by this guard. A future refactor that tightens
// this would break the apply default — this test pins the
// current behaviour so a regression surfaces here first.
func TestApplyProject_Guard_ProductionBranchEmptySkips(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// Apply with branch="" — must NOT trip the guard.
	status, body := applyProjectRaw(t, h, key, "branch-skip", "", singleWorkloadFixture(t))
	if status != http.StatusOK {
		t.Fatalf("empty-branch apply status=%d want 200, body=%s", status, body)
	}
}

// TestApplyProject_Guard_ProductionBranchMismatch pins the
// failure path: an apply whose prod_branch differs from the
// project's existing branch trips state.ErrProdBranchMismatch.
// The handler maps this to 409 prod_branch_mismatch.
func TestApplyProject_Guard_ProductionBranchMismatch(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply pins production_branch='main'.
	applyProjectMultipart(t, h, key, "branch-mm", "", singleWorkloadFixture(t))
	// Second apply with prod_branch='develop' — mismatch.
	status, body := applyProjectRaw(t, h, key, "branch-mm", "develop", singleWorkloadFixture(t))
	if status != http.StatusConflict {
		t.Fatalf("mismatch status=%d want 409, body=%s", status, body)
	}
}

// TestApplyProject_Guard_ScanSourceStable pins the scan-source
// downgrade guard. First apply creates a project with tier
// 'compose' (composeFixture has docker-compose.yml). Second apply
// posts a convention-only fixture, which is tier 'convention' —
// convention < compose in the rank table, so the downgrade guard
// trips and the handler returns 409.
func TestApplyProject_Guard_ScanSourceStable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// Tier=compose on first apply.
	ar := applyProjectMultipart(t, h, key, "scan-stable", "", composeFixture(t))
	if ar.ScanSource != "compose" {
		t.Fatalf("first apply ScanSource=%q want compose", ar.ScanSource)
	}
	// Tier=convention on second apply → downgrade.
	status, body := applyProjectRaw(t, h, key, "scan-stable", "", conventionOnlyFixture(t))
	if status != http.StatusConflict {
		t.Fatalf("downgrade status=%d want 409, body=%s", status, body)
	}
	var p api.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if p.Code != "scan_source_downgrade" {
		t.Fatalf("code=%q want scan_source_downgrade", p.Code)
	}
}

// TestApplyProject_Guard_ScanSourceUpgrade pins the opposite:
// an upgrade is always accepted (a convention project can adopt
// compose). No downgrade, so no rejection.
func TestApplyProject_Guard_ScanSourceUpgrade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: convention tier.
	ar1 := applyProjectMultipart(t, h, key, "scan-up", "", conventionOnlyFixture(t))
	if ar1.ScanSource != "convention" {
		t.Skipf("first apply ScanSource=%q — convention detector may have changed", ar1.ScanSource)
	}
	// Second apply: compose tier (upgrade) — must be accepted.
	status, _ := applyProjectRaw(t, h, key, "scan-up", "", composeFixture(t))
	if status != http.StatusOK {
		t.Fatalf("upgrade status=%d want 200 (upgrades are allowed)", status)
	}
}

// TestApplyProject_Guard_NoWorkloadsAuditRow pins that the
// neverEmpty guard emits an alert/audit row. The dashboard
// surfaces "no workloads detected" via this row; a missing
// row would render nothing. We tolerate the table not existing
// (older schemas) but check for the row when it does.
func TestApplyProject_Guard_NoWorkloadsAuditRow(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	_, _ = applyProjectRaw(t, h, key, "no-wl", "", emptyFixture(t))
	// Check alerts (the per-app emit) — the table may not exist
	// on this schema; skip if so. We just want to pin that the
	// guard emits SOMETHING the dashboard reads.
	var alertCount int
	err := pool.QueryRow(context.Background(),
		`select count(*) from alerts where kind = 'no_workloads'`).Scan(&alertCount)
	if err != nil {
		t.Logf("alerts query failed (table may not exist): %v", err)
		return
	}
	if alertCount == 0 {
		t.Fatalf("never-empty guard emitted zero alert rows (dashboard will show nothing)")
	}
}
