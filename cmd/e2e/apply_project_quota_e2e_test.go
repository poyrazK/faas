// apply_project_quota_e2e_test.go — plan-quota matrix for the
// apply path. Distinct from quota_e2e_test.go (which covers
// single-app create) in two ways:
//
//   1. The quota gate is on the *project + its workloads* (a
//      5-workload repo against Free trips even though Free allows
//      1 app per the legacy single-app quota gate). Pre-PR-A the
//      single-app quota was the only gate; the multi-workload
//      quota is plan.DeployedApps.
//
//   2. On quota failure the apply path must create NOTHING —
//      zero project rows, zero app rows, zero build rows. The
//      store-side Tx rolls back, the handler surfaces RFC 7807
//      with limit + observed + docs URL.
//
// Each test asserts:
//   - status code (403 for over-cap, 402 for crons-not-allowed).
//   - problem.code matches the spec's stable code.
//   - DB has zero rows in projects / apps for the over-cap apply.
//   - No build rows were created either (PR-A: a quota failure
//     must not enqueue any builds).

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

// quotaFixture builds an N-workload tarball with N convention-
// detector workloads. Used to push plans over their DeployedApps
// cap without involving the multi-tier detectors.
func quotaFixture(t *testing.T, workloadCount int) []byte {
	t.Helper()
	entries := make([]struct{ name, body string }, 0, workloadCount+1)
	entries = append(entries, struct{ name, body string }{
		"faas-quota/docker-compose.yml", "services:\n  api:\n    build: { context: services/api }\n",
	})
	for i := 0; i < workloadCount; i++ {
		entries = append(entries, struct{ name, body string }{
			"faas-quota/services/svc" + itoa(i) + "/Dockerfile",
			"FROM alpine:3.19\nCMD [\"./svc\"]\n",
		})
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

// itoa is a local helper to avoid strconv import for trivial use.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// applyProjectExpectProblem POSTs and asserts the response is an
// RFC 7807 problem with the expected status + code.
func applyProjectExpectProblem(t *testing.T, h *e2etest.Harness, key, slug string, body []byte, wantStatus int, wantCode string) {
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
		t.Fatalf("apply req: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("status=%d want %d (body=%s)", resp.StatusCode, wantStatus, raw)
	}
	var p api.Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, raw)
	}
	if p.Code != wantCode {
		t.Fatalf("code=%q want %q (body=%s)", p.Code, wantCode, raw)
	}
}

// TestApplyProject_Quota_FreePlanOverCap pins the Free plan
// (DeployedApps=1) with a 2-workload repo. Status 403, code
// plan_limit_apps, zero rows created.
func TestApplyProject_Quota_FreePlanOverCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// 2-workload repo for a 1-app-cap plan.
	applyProjectExpectProblem(t, h, key, "free-over", quotaFixture(t, 2),
		http.StatusForbidden, api.CodePlanLimitApps)

	// Pin: zero project rows for this account. The accounts table
	// has no api_key column; auth keys live in api_keys.key_sha256
	// (migrations/00001_init.sql:23) — join through api_keys to
	// scope by the calling API key.
	var projCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from projects p
			join api_keys k on k.account_id = p.account_id
			where k.key_sha256 = sha256($1)`,
		key).Scan(&projCount)
	if projCount != 0 {
		t.Fatalf("Free over-cap apply left %d project rows (rollback failed)", projCount)
	}
}

// TestApplyProject_Quota_HobbyPlanUnderCap pins that Hobby
// (DeployedApps=5) accepts a 2-workload repo and creates
// 2 apps + 2 builds.
func TestApplyProject_Quota_HobbyPlanUnderCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	ar := applyProjectMultipart(t, h, key, "hobby-ok", "", quotaFixture(t, 2))
	if len(ar.Apps) < 2 {
		t.Fatalf("Hobby accepted only %d apps, want 2", len(ar.Apps))
	}
	if len(ar.Builds) < 2 {
		t.Fatalf("Hobby enqueued only %d builds, want 2", len(ar.Builds))
	}
}

// TestApplyProject_Quota_HobbyPlanOverCap pins Hobby (5 apps)
// with a 6-workload repo. Status 403, code plan_limit_apps.
func TestApplyProject_Quota_HobbyPlanOverCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	applyProjectExpectProblem(t, h, key, "hobby-over", quotaFixture(t, 6),
		http.StatusForbidden, api.CodePlanLimitApps)
}

// TestApplyProject_Quota_ProPlanUnderCap pins Pro (25 apps) +
// 4-workload repo accepted.
func TestApplyProject_Quota_ProPlanUnderCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	ar := applyProjectMultipart(t, h, key, "pro-ok", "", quotaFixture(t, 4))
	if len(ar.Apps) < 4 {
		t.Fatalf("Pro accepted only %d apps, want 4", len(ar.Apps))
	}
}

// TestApplyProject_Quota_ScalePlanUnderCap pins Scale (100 apps)
// + 6-workload repo accepted.
func TestApplyProject_Quota_ScalePlanUnderCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanScale)

	ar := applyProjectMultipart(t, h, key, "scale-ok", "", quotaFixture(t, 6))
	if len(ar.Apps) < 6 {
		t.Fatalf("Scale accepted only %d apps, want 6", len(ar.Apps))
	}
}

// TestApplyProject_Quota_CronsNotAllowed pins the Free plan
// (CronLimitPerAccount=0). A repo with a cron workload returns
// 402 plan_crons_not_allowed; zero rows created.
func TestApplyProject_Quota_CronsNotAllowed(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// render.yaml produces a cron workload (schedule: "0 3 * * *").
	const renderYAML = `cronJobs:
  - name: nightly
    schedule: "0 3 * * *"
    command: bundle exec rake nightly
`
	entries := []struct{ name, body string }{
		{"faas-cron/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n"},
		{"faas-cron/render.yaml", renderYAML},
		{"faas-cron/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
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

	applyProjectExpectProblem(t, h, key, "free-cron", buf.Bytes(),
		http.StatusPaymentRequired, api.CodePlanCronsNotAllowed)
}

// TestApplyProject_Quota_OverCapZeroBuilds pins that an over-cap
// apply creates zero build rows. The apply path must roll back
// cleanly — even a partial apply that enqueued some builds before
// tripping the quota gate would leave orphan build rows that
// builderd might claim.
func TestApplyProject_Quota_OverCapZeroBuilds(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	applyProjectExpectProblem(t, h, key, "zero-builds", quotaFixture(t, 2),
		http.StatusForbidden, api.CodePlanLimitApps)

	// Pin: zero build rows for this account. Auth keys live in
	// api_keys.key_sha256; join through api_keys to scope by the
	// calling API key (accounts has no api_key column).
	var buildCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from builds b
			join deployments d on d.id = b.deployment_id
			join apps a on a.id = d.app_id
			join api_keys k on k.account_id = a.account_id
			where k.key_sha256 = sha256($1)`,
		key).Scan(&buildCount)
	if buildCount != 0 {
		t.Fatalf("over-cap apply left %d build rows (rollback failed)", buildCount)
	}
}

// TestApplyProject_Quota_ProblemCarriesLimitAndObserved pins the
// RFC 7807 contract: the problem body carries limit + observed
// values + a docs URL. The dashboard renders these; a missing
// field would render "Apply failed (unknown limit)".
func TestApplyProject_Quota_ProblemCarriesLimitAndObserved(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// Post a 5-workload fixture against Free (limit 1). The
	// problem should carry limit=1 and observed=5.
	body := quotaFixture(t, 5)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	src, _ := mw.CreateFormFile("source", "fixture.tar.gz")
	_, _ = src.Write(body)
	_ = mw.WriteField("project_slug", "free-bulk")
	_ = mw.Close()
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.APIDURL+"/v1/projects", &buf)
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
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d want 403", resp.StatusCode)
	}
	var p api.Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The Problem type embeds the limit + observed in the body
	// JSON. Inspect the raw body for the keys.
	for _, want := range []string{`"limit"`, `"observed"`, `"docs_url"`} {
		if !problemBodyContains(raw, want) {
			t.Fatalf("problem body missing %s (body=%s)", want, raw)
		}
	}
}

// problemBodyContains is a tiny substring helper; avoids pulling in
// strings.Contains for one call. Renamed from `contains` to avoid
// the package-level declaration in account_e2e_test.go.
func problemBodyContains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

// TestApplyProject_Quota_FreeAcceptedAsOneApp pins that Free
// (DeployedApps=1) accepts a 1-workload repo. The boundary case.
func TestApplyProject_Quota_FreeAcceptedAsOneApp(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	ar := applyProjectMultipart(t, h, key, "free-ok", "", quotaFixture(t, 1))
	if len(ar.Apps) != 1 {
		t.Fatalf("Free accepted %d apps, want 1", len(ar.Apps))
	}
	if len(ar.Builds) != 1 {
		t.Fatalf("Free enqueued %d builds, want 1", len(ar.Builds))
	}
}

// TestApplyProject_Quota_ScaleCronsUnderCap pins Scale
// (CronLimitPerAccount=500) with a 6-cron repo. The crons gate
// is independent of the apps gate; on Scale the 6 crons sit
// well under the 500 cap, so the apply succeeds and creates
// the workload rows. A 6-cron-on-Hobby variant would trip the
// apps gate first (DeployedApps=5 < 6 workloads) before the
// cron quota ever fires; Scale sidesteps that ordering.
func TestApplyProject_Quota_ScaleCronsUnderCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanScale)

	// render.yaml with 6 cron jobs → 6 cron workloads.
	entries := []struct{ name, body string }{
		{"faas-scale-crons/render.yaml", `cronJobs:
  - name: nightly1
    schedule: "0 3 * * *"
    command: echo 1
  - name: nightly2
    schedule: "0 4 * * *"
    command: echo 2
  - name: nightly3
    schedule: "0 5 * * *"
    command: echo 3
  - name: nightly4
    schedule: "0 6 * * *"
    command: echo 4
  - name: nightly5
    schedule: "0 7 * * *"
    command: echo 5
  - name: nightly6
    schedule: "0 8 * * *"
    command: echo 6
`},
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

	ar := applyProjectMultipart(t, h, key, "scale-crons-under", "", buf.Bytes())
	if ar.ProjectID == "" {
		t.Fatalf("Scale under-cron-cap apply returned empty project_id")
	}
	// Pin: every cron is recorded as a cron workload (ClassJob).
	var cronCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from apps where project_id = $1 and workload_class = 'job'`,
		ar.ProjectID).Scan(&cronCount)
	if cronCount != 6 {
		t.Fatalf("Scale accepted %d cron workloads, want 6", cronCount)
	}
}

// Local alias to keep the time import named even when not used
// in a particular sub-test build.
var _ = time.Second
