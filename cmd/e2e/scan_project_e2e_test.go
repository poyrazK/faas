// scan_project_e2e_test.go — non-metal CI-safe acceptance for the
// Phase 3 wire contract on POST /v1/projects/scan.
//
// What this test pins
//
//   - The end-to-end pipeline that takes a customer tarball at the
//     wire surface and produces a `PlanResponse` whose shape
//     matches pkg/api.PlanResponse. This is the bridge the unit
//     suite (`pkg/reposcan/scan_test.go`) cannot reach: the
//     handler does multipart parse → spool → validate →
//     extract → reposcan.Scan → quota gate → plan_token. Each
//     step has its own unit tests; only an httptest+Postgres
//     harness exercises them together.
//
//   - The repo-decomposition §4 fixture expectation: a tarball
//     carrying compose + Procfile + fly.toml + render.yaml +
//     convention dirs produces an 8-entry / 6-unique-name plan
//     (api from compose, web/worker from Procfile, faas-fixture
//     from fly.toml, nightly cronJob from render.yaml, cron
//     (Procfile class=job, no Schedule), api + worker from
//     convention services/); the (RootDir, Name) merge key keeps
//     the two `api` and two `worker` entries separate but they
//     collapse under the unique-name view. One managed entry
//     (postgres db), tier=compose, can_apply=true on the Pro
//     plan (6 unique apps under the 25 cap, 1 cron under the
//     20 cap). Only render.yaml's nightly carries a Schedule, so
//     only nightly promotes to planCron — see the cron assertion
//     in TestScanProject_MultiTierFixture_UnderQuota.
//
//   - The over-quota gate: Free plan returns `can_apply=false`
//     and `crons_not_allowed=true` for the same fixture (Free
//     cron cap is 0; the render.yaml nightly cronJob trips the
//     gate).
//
//   - The --only filter: posting `only=web,api` produces a
//     2-workload plan while the managed entry remains visible
//     (--only does NOT filter managed; spec §4).
//
// What this test deliberately does NOT cover
//
//   - The actual `POST /v1/projects` apply path — covered by
//     `signed_deploy_e2e_test.go` for the trusted-signer gate and
//     by `quota_e2e_test.go` for the limit envelope. Driving the
//     apply path through here would couple two large surfaces in
//     one test and lose focus.
//
//   - The metal side (vmmd, schedd, gatewayd, imaged, meterd,
//     builderd) — not started. The scan endpoint never wakes an
//     instance; only apid runs.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS or when pgtest.Open returns nil).

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
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// scanProjectFixture returns the bytes of a multi-tier customer
// tarball. The shape mirrors a `git archive` of a real backend
// monorepo: four Tier 1 sources (compose + Procfile + fly.toml +
// render.yaml) and two Tier 3 convention members
// (services/{api,worker}/Dockerfile).
//
// Convention detector notes (pkg/reposcan/convention.go):
//   - The detector walks `services/`, `apps/`, `packages/`, `cmd/`
//     as TOP-LEVEL entries of the fsys — putting them under a
//     `faas-fixture/` prefix means the detector never sees them.
//   - The emitted workload Name is e.Name() — the bare directory
//     name, NOT the full path. So `services/api` produces a
//     workload named `api` (with RootDir="services/api"), which
//     is a distinct (RootDir, Name) merge key from compose's
//     `api` (RootDir=""). Both stay in the plan.
//
// The total compressed size is well under the smallest plan's
// 100 MB cap (memory: pkg/api/limits.go SourceTarballMaxMB[PlanFree]
// = 100).
func scanProjectFixture(t *testing.T) []byte {
	t.Helper()
	const composeYML = `services:
  api:
    build:
      context: .
    environment:
      DATABASE_URL: postgres://localhost/api
      REDIS_URL: redis://localhost:6379
    ports:
      - "8080:8080"
  worker:
    build:
      context: .
    environment:
      DATABASE_URL: postgres://localhost/api
      REDIS_URL: redis://localhost:6379
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: example
`
	const procfile = `web:    bundle exec rails server -p $PORT
worker: bundle exec sidekiq
cron:   bundle exec rake nightly
release: bundle exec rake assets:precompile
`
	const flyTOML = `app = "faas-fixture"
primary_region = "fra"

[build]

[env]
  PORT = "8080"

[[services]]
  internal_port = 8080
  protocol = "tcp"
  [[services.ports]]
    port = 80
    handlers = ["http"]
`
	// render.yaml carries a CronJob that surfaces a real
	// Schedule on the workload — this is the only detector
	// (besides k8s CronJob + app.yaml + serverless.yml) that
	// emits a non-empty Schedule, which is what the
	// scan_service cron-promotion logic keys on. Procfile's
	// `cron:` line does NOT carry a schedule expression
	// (pkg/reposcan/procfile.go: detectProcfile never sets
	// schedule); the workload gets class=job but no Schedule,
	// so the scan service skips it in planCron construction.
	const renderYAML = `cronJobs:
  - name: nightly
    schedule: "0 3 * * *"
    command: bundle exec rake nightly
`
	// entries is the tar payload as an ordered slice so the tar
	// header sequence is deterministic across runs. The
	// cmd/apid/extract.go extractor (extractTarGzInto) strips a
	// single top-level prefix from every entry, derived from the
	// FIRST header it sees — so the order of these entries is
	// load-bearing. Top-level (no-slash) entries must come before
	// the services/{api,worker}/ entries: if `services/api/Dockerfile`
	// landed first, the extractor would treat `services` as the
	// archive root and strip it from the subsequent services/*/file
	// entries, collapsing `services/api/Dockerfile` to `api/Dockerfile`
	// at the spool root — and the convention detector would never
	// see a `services/` directory. A map[string]string iteration
	// here would be randomized and trip the flake approximately
	// half the time. This mirrors what `git archive` produces for
	// real customer repos: the top-level config files appear
	// before the nested service dirs.
	entries := []struct {
		name, body string
	}{
		{"faas-fixture/docker-compose.yml", composeYML},
		{"faas-fixture/Procfile", procfile},
		{"faas-fixture/fly.toml", flyTOML},
		{"faas-fixture/render.yaml", renderYAML},
		{"faas-fixture/services/api/Dockerfile", "FROM alpine:3.19\nEXPOSE 8080\nCMD [\"./api\"]\n"},
		{"faas-fixture/services/worker/Dockerfile", "FROM alpine:3.19\nCMD [\"./worker\"]\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar write %s: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// scanProjectMultipart POSTs to /v1/projects/scan with the given
// `source` bytes + form fields and returns the parsed PlanResponse
// (or fails the test). Multipart wiring mirrors the wire contract
// documented at cmd/apid/scan_service.go:461-525: field `source`
// is the tarball; `project_slug` is the kebab identifier; `only`
// is the comma-separated workload filter (empty string omits).
func scanProjectMultipart(t *testing.T, h *e2etest.Harness, key, slug, only string, body []byte) api.PlanResponse {
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
	ctx, cancel := context.WithTimeout(req.Context(), scanRequestTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("scan request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", resp.StatusCode, raw)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode PlanResponse: %v (body=%s)", err, raw)
	}
	return plan
}

// workloadNames returns the workload names of a plan sorted
// ascending, with duplicates collapsed. The merge key
// (RootDir, Name) keeps a Procfile `worker` (root="") and a
// compose `worker` (root="services/worker") as two distinct
// plan entries — same Name, different key. Most tests want the
// "what user-facing workloads will be created" view, which is
// the unique-name set.
func workloadNames(plan api.PlanResponse) []string {
	seen := make(map[string]bool, len(plan.Workloads))
	for _, w := range plan.Workloads {
		seen[w.Name] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// TestScanProject_MultiTierFixture_UnderQuota pins the §4 fixture
// expectation on the Pro plan (apps under cap, crons under cap).
// The fixture trips every Tier 1 detector (compose + Procfile +
// fly.toml + render.yaml) and the Tier 3 convention detector
// (services/{api,worker}/Dockerfile). The plan surface carries 8
// workload entries:
//
//   - api          (compose, rootDir="")
//   - api          (convention, rootDir="services/api")
//   - cron         (Procfile, rootDir="", class=job, Schedule="")
//   - faas-fixture (fly.toml, rootDir="")
//   - nightly      (render.yaml cronJob, rootDir="", class=job,
//     Schedule="0 3 * * *")
//   - web          (Procfile, rootDir="")
//   - worker       (Procfile, rootDir="")
//   - worker       (convention, rootDir="services/worker")
//
// Unique workload Names (6): api, cron, faas-fixture, nightly,
// web, worker. Plus the postgres db managed entry and tier=compose.
//
// The (RootDir, Name) merge key keeps the two `api` and two
// `worker` entries separate — see pkg/reposcan/scan.go Workload.Key.
func TestScanProject_MultiTierFixture_UnderQuota(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Override the apid spool roots to the per-test tmp so the test
	// never touches /var/spool/faas (no root needed).
	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "source"),
			"FAAS_SCAN_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	key := h.SeedAccount(context.Background(), api.PlanPro, "scan-under-quota")

	plan := scanProjectMultipart(t, h, key, "multi-tier", "", scanProjectFixture(t))

	if plan.ProjectSlug != "multi-tier" {
		t.Errorf("ProjectSlug = %q, want multi-tier", plan.ProjectSlug)
	}
	if plan.Tier != "compose" {
		// Note: scanPlanResponse.Tier is a JSON string; reposcan.Tier
		// is a typed int whose String() returns "compose".
		t.Errorf("Tier = %q, want compose", plan.Tier)
	}
	if !plan.CanApply {
		t.Errorf("CanApply = false on Pro plan (apps under cap, crons under cap); observed=%d limit=%d",
			plan.ObservedApps, plan.LimitApps)
	}
	if plan.CronsNotAllowed {
		t.Errorf("CronsNotAllowed = true on Pro plan; cron cap is %d", plan.LimitCrons)
	}
	wantNames := []string{"api", "cron", "faas-fixture", "nightly", "web", "worker"}
	if got := workloadNames(plan); !equalStrings(got, wantNames) {
		t.Errorf("workload names mismatch:\n got: %v\nwant: %v", got, wantNames)
	}
	// Per-workload field spot checks. Key by (RootDir, Name) so
	// the compose api (rootDir=".", the literal `context: .` from
	// the fixture) and the convention api (rootDir="services/api")
	// stay distinct — both surfaces are load-bearing for different
	// assertions below.
	byKey := map[string]api.PlanWorkload{}
	for _, w := range plan.Workloads {
		byKey[w.RootDir+"\x00"+w.Name] = w
	}
	composeAPI := byKey[".\x00api"]
	if composeAPI.Class != "unknown" || len(composeAPI.Ports) != 1 || composeAPI.Ports[0] != 8080 {
		t.Errorf("compose api workload = %+v; want class=unknown, ports=[8080]", composeAPI)
	}
	conventionAPI := byKey["services/api\x00api"]
	if conventionAPI.Class != "unknown" || len(conventionAPI.Ports) != 0 {
		t.Errorf("convention api workload = %+v; want class=unknown, ports=[]", conventionAPI)
	}
	if w := byKey["\x00cron"]; w.Class != "job" {
		t.Errorf("cron workload class = %q, want job", w.Class)
	}
	if w := byKey["\x00web"]; w.Class != "http" || len(w.Command) == 0 {
		t.Errorf("web workload = %+v; want class=http, non-empty Command", w)
	}
	// Managed: exactly the postgres db from compose.image:.
	if len(plan.Managed) != 1 || plan.Managed[0].Name != "db" ||
		plan.Managed[0].Kind != "postgres" || plan.Managed[0].EnvHint != "DATABASE_URL" {
		t.Errorf("managed = %+v; want single entry {db, postgres, DATABASE_URL}", plan.Managed)
	}
	// Crons: the render.yaml nightly cronJob is the only entry
	// that carries a Schedule, so the scan_service cron-promotion
	// loop picks it up. Procfile's `cron:` line has class=job
	// but Schedule="" (no expression) and is therefore skipped at
	// scan_service.go:234 — that's expected behaviour, not a bug.
	var sawCron bool
	for _, c := range plan.Crons {
		if c.WorkloadName == "nightly" {
			sawCron = true
			if c.Schedule != "0 3 * * *" {
				t.Errorf("cron schedule = %q, want \"0 3 * * *\"", c.Schedule)
			}
			if !c.Enabled || c.Path != "/" {
				t.Errorf("cron planCron = %+v; want Enabled=true Path=/", c)
			}
		}
	}
	if !sawCron {
		t.Errorf("expected planCron entry for workload_name=nightly; got: %+v", plan.Crons)
	}
	if plan.PlanToken == "" {
		t.Errorf("PlanToken empty; scan service must mint a token even on the dry-run path")
	}
}

// TestScanProject_FreePlan_CronNotAllowed pins the Free-plan
// cron gate: the same fixture must surface can_apply=false and
// crons_not_allowed=true because the Free plan has CronLimitPerAccount=0
// (per pkg/api/limits.go PlanFree). The app-count check stays
// under cap (5/1), so the only failing axis is the cron gate.
func TestScanProject_FreePlan_CronNotAllowed(t *testing.T) {
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
	key := h.SeedAccount(context.Background(), api.PlanFree, "scan-free-cron")

	plan := scanProjectMultipart(t, h, key, "multi-tier-free", "", scanProjectFixture(t))

	if plan.CanApply {
		t.Errorf("CanApply = true on Free plan with cron workload; want false")
	}
	if !plan.CronsNotAllowed {
		t.Errorf("CronsNotAllowed = false on Free plan; want true (CronLimitPerAccount=0)")
	}
	// Workloads still discovered (the scan path runs regardless of
	// quota) — the gate is on apply, not scan.
	if len(plan.Workloads) == 0 {
		t.Errorf("Free plan scan returned 0 workloads; expected full plan")
	}
	// Managed still surfaces db (a customer on Free who attempts
	// provision still needs to see what is and isn't going to be
	// created).
	var sawDB bool
	for _, m := range plan.Managed {
		if m.Name == "db" && m.Kind == "postgres" {
			sawDB = true
		}
	}
	if !sawDB {
		t.Errorf("managed db entry missing on Free plan scan; got: %+v", plan.Managed)
	}
}

// TestScanProject_OnlyFilter_ManagedUnaffected pins that the
// --only filter narrows the workloads list but NEVER narrows the
// managed list (spec §4: stateful services the platform will not
// provision must always surface so the customer sees the warning
// signal). The filter accepts a comma-separated workload name
// list; entries not matching are dropped.
func TestScanProject_OnlyFilter_ManagedUnaffected(t *testing.T) {
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
	key := h.SeedAccount(context.Background(), api.PlanPro, "scan-only-filter")

	// only=web,api narrows to two workloads; the others (cron,
	// faas-fixture, services/api, services/worker, worker) drop.
	plan := scanProjectMultipart(t, h, key, "only-test", "web,api", scanProjectFixture(t))

	wantNames := []string{"api", "web"}
	if got := workloadNames(plan); !equalStrings(got, wantNames) {
		t.Errorf("only-filtered workload names:\n got: %v\nwant: %v", got, wantNames)
	}
	// Managed untouched.
	if len(plan.Managed) != 1 || plan.Managed[0].Name != "db" {
		t.Errorf("only-filtered managed = %+v; want single db entry (--only does not filter managed)", plan.Managed)
	}
}

// TestScanProject_NestedTarballEntries pins the post-#528 fix on
// the wire surface: a tarball whose entries share a common
// top-level prefix (the canonical `git archive` shape — every
// real customer repo produces this) must succeed end-to-end
// through the spool → validate → extract → scan pipeline. Pre-
// fix, the absolute-spool case rejected every nested entry at
// extract.go:215 with `path escape after join rejected`. This
// test wraps the regression in the wire surface so a future
// regression in the multipart-extract-seam pair is caught at CI
// time, not on the EX44 box.
func TestScanProject_NestedTarballEntries(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Override BOTH spool roots so the test exercises the
	// absolute-path code path (the post-#528 regression shape).
	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{
			"FAAS_SPOOL_ROOT=" + tmpSpool(t, "source"),
			"FAAS_SCAN_SPOOL_ROOT=" + tmpSpool(t, "scan"),
		})
	key := h.SeedAccount(context.Background(), api.PlanPro, "scan-nested")

	plan := scanProjectMultipart(t, h, key, "nested", "", scanProjectFixture(t))

	if plan.ProjectSlug != "nested" {
		t.Errorf("ProjectSlug = %q, want nested", plan.ProjectSlug)
	}
	if len(plan.Workloads) == 0 {
		t.Errorf("nested-entry scan returned 0 workloads; the post-#528 fix landed but wire contract regressed")
	}
	// The fixture carries `services/api/Dockerfile` (nested two
	// levels under repo root); the convention detector must surface
	// the corresponding workload. The detector emits Name=e.Name()
	// (just `api`) with RootDir="services/api", so the
	// load-bearing wire assertion looks for the RootDir tuple,
	// not the bare name. This is what pins the post-#528 nested-
	// entry seam from the wire surface — a future regression in
	// the multipart-extract path that drops nested entries would
	// drop this workload entirely (compose's `api` has rootDir=""
	// and is a distinct entry under the (RootDir, Name) merge
	// key).
	var sawServicesAPI bool
	for _, w := range plan.Workloads {
		if w.RootDir == "services/api" {
			sawServicesAPI = true
		}
	}
	if !sawServicesAPI {
		t.Errorf("expected convention-detected workload with root_dir=services/api (nested entry seam); workloads = %+v",
			plan.Workloads)
	}
}

// equalStrings reports whether two sorted string slices contain
// the same entries. Avoids importing reflect in tests.
func equalStrings(a, b []string) bool {
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

// tmpSpool returns a path under the test's t.TempDir() so
// apid's spool validation can write into it without root. The
// `label` suffix lets multiple sub-tests share one Harness without
// colliding on the same spool dir (which is fine for parallel
// sub-tests because each gets a fresh tmpdir). Mirrors the
// pattern at cmd/apid/extract_test.go:42-78 where t.Setenv
// stamps FAAS_SCAN_SPOOL_ROOT before the first request.
func tmpSpool(t *testing.T, label string) string {
	t.Helper()
	return t.TempDir() + "/" + label
}

// scanRequestTimeout bounds the multipart upload + scan handler.
// The scan handler itself is sub-second (extract + reposcan on a
// 5-entry fixture), but a cold Mac dev box or a CI runner with
// noisy neighbours can take 5–10s. Matches the 10s bound on
// quota_e2e_test.go:127 (`doReq`).
const scanRequestTimeout = 10 * time.Second
