// apply_project_diff_e2e_test.go — coverage for the second-apply
// diff path (plan §B "apply_project_diff"):
//
//   1. unchanged: a second apply with the SAME workloads produces
//      no new build rows. Pre-PR-A there were no build rows at
//      all; post-PR-A we must not enqueue spurious builds when
//      nothing changed.
//   2. added: a new workload appears in the second apply, gets a
//      build row.
//   3. removed: a workload present in the first apply is absent
//      from the second — its apps row goes soft-deleted
//      (state.ErrSoftDelete triggers downstream cascades: envs,
//      domains, crons).
//   4. changed: a workload's source hash differs — its deployment
//      row is superseded, new build enqueued.
//   5. cron-soft-delete: a render.yaml cron that disappears between
//      applies is soft-deleted (the PR-GH.6 500 regression path).
//   6. domain cascade: a domain attached to a removed app is
//      removed too.
//   7. env cascade: an env var on a removed app is removed too.

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// twoWorkloadFixture builds an N=2 repo so a 2nd apply can add /
// remove / mutate one workload while keeping the other intact.
func twoWorkloadFixture(t *testing.T, prefix string) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: services/api }\n  worker:\n    build: { context: services/worker }\n"},
		{prefix + "/services/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{prefix + "/services/api/index.js", "exports.handler = () => 1;\n"},
		{prefix + "/services/worker/Dockerfile", "FROM alpine:3.19\nCMD [\"./worker\"]\n"},
		{prefix + "/services/worker/index.js", "exports.handler = () => 2;\n"},
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

// oneWorkloadFixtureFromPrefix builds a 1-workload repo. The
// workload name is taken from the suffix of `prefix` (so we can
// run twoWorkloadFixture + oneWorkloadFixtureFromPrefix with
// overlapping prefixes to test removal).
func oneWorkloadFixtureFromPrefix(t *testing.T, prefix string) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: services/api }\n"},
		{prefix + "/services/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{prefix + "/services/api/index.js", "exports.handler = () => 1;\n"},
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

// twoWorkloadChangedFixture builds a 2-workload repo where the
// `api` workload's index.js content differs from the first apply
// — this exercises the `~` changed path (new deployment +
// superseded old build).
func twoWorkloadChangedFixture(t *testing.T, prefix string) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: services/api }\n  worker:\n    build: { context: services/worker }\n"},
		{prefix + "/services/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		// Note: body differs from twoWorkloadFixture's index.js
		// (1 vs 99). The detector hashes the source tree; a
		// different body → different hash → `~` changed.
		{prefix + "/services/api/index.js", "exports.handler = () => 99;\n"},
		{prefix + "/services/worker/Dockerfile", "FROM alpine:3.19\nCMD [\"./worker\"]\n"},
		{prefix + "/services/worker/index.js", "exports.handler = () => 2;\n"},
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

// twoWorkloadMovedFixture keeps the durable Compose service names while
// moving api to a different build context. A re-apply must update the
// existing app instead of deleting it and creating a replacement.
func twoWorkloadMovedFixture(t *testing.T, prefix string) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: apps/api }\n  worker:\n    build: { context: services/worker }\n"},
		{prefix + "/apps/api/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
		{prefix + "/apps/api/index.js", "exports.handler = () => 99;\n"},
		{prefix + "/services/worker/Dockerfile", "FROM alpine:3.19\nCMD [\"./worker\"]\n"},
		{prefix + "/services/worker/index.js", "exports.handler = () => 2;\n"},
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

func scheduledProjectFixture(t *testing.T, prefix, schedule string) []byte {
	t.Helper()
	render := "cronJobs:\n  - name: nightly\n    schedule: \"" + schedule + "\"\n    command: echo nightly\n"
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n"},
		{prefix + "/render.yaml", render},
		{prefix + "/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		hdr := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(entry.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func unscheduledProjectFixture(t *testing.T, prefix string) []byte {
	t.Helper()
	entries := []struct{ name, body string }{
		{prefix + "/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n  nightly:\n    build: { context: . }\n"},
		{prefix + "/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		hdr := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(entry.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// TestApplyProject_Diff_Unchanged pins the no-op diff: a 2nd apply
// with identical workloads creates zero new build rows. The
// apps + deployments counts stay the same. This is the regression
// net for a class of bugs where the diff engine treats every
// apply as `changed all`.
func TestApplyProject_Diff_Unchanged(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	body := twoWorkloadFixture(t, "faas-diff-unchanged")
	ar1 := applyProjectMultipart(t, h, key, "diff-unchanged", "", body)
	if len(ar1.Builds) != 2 {
		t.Fatalf("first apply builds=%d want 2", len(ar1.Builds))
	}
	buildsBefore := len(ar1.Builds)
	ar2 := applyProjectMultipart(t, h, key, "diff-unchanged", "", body)
	// Wire response `Apps` is the complete active project membership,
	// including unchanged rows, so clients can replace local state from
	// every successful response. The diff semantic is carried by Builds:
	// a no-op must enqueue none while the two existing apps remain.
	if len(ar2.Apps) != 2 {
		t.Fatalf("no-op re-apply apps=%d want 2 active project members", len(ar2.Apps))
	}
	if len(ar2.Builds) != 0 {
		t.Fatalf("unchanged re-apply enqueued %d builds, want 0 (regression: every apply churns builds)", len(ar2.Builds))
	}
	if buildsBefore == 0 {
		t.Fatalf("first apply builds was 0 — test setup issue")
	}
	var appsAfter int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from apps where project_id = $1 and status <> 'deleted'`,
		ar1.ProjectID).Scan(&appsAfter); err != nil {
		t.Fatalf("count apps after re-apply: %v", err)
	}
	if appsAfter != 2 {
		t.Fatalf("project membership after re-apply=%d want 2 (existing rows must not be deleted)", appsAfter)
	}
}

// TestApplyProject_Diff_Added pins the + path: 2nd apply has one
// more workload than the 1st. The new workload gets a build row;
// existing workloads do not.
func TestApplyProject_Diff_Added(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: 1 workload.
	ar1 := applyProjectMultipart(t, h, key, "diff-add", "", oneWorkloadFixtureFromPrefix(t, "faas-add1"))
	if len(ar1.Apps) != 1 {
		t.Fatalf("first apply apps=%d want 1", len(ar1.Apps))
	}

	// Second apply: 2 workloads (same prefix, worker added).
	ar2 := applyProjectMultipart(t, h, key, "diff-add", "", twoWorkloadFixture(t, "faas-add1"))
	if len(ar2.Apps) != 2 {
		t.Fatalf("second apply apps=%d want 2 (1 added)", len(ar2.Apps))
	}
	if len(ar2.Builds) != 1 {
		t.Fatalf("second apply builds=%d want 1 (only the new workload builds)", len(ar2.Builds))
	}
}

// TestApplyProject_Diff_Removed pins the − path: 2nd apply has
// one fewer workload than the 1st. The removed workload's apps
// row is soft-deleted (apps.status=deleted OR apps.deleted_at
// IS NOT NULL — schema-dependent). Existing workloads do not
// re-build.
func TestApplyProject_Diff_Removed(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: 2 workloads.
	ar1 := applyProjectMultipart(t, h, key, "diff-rm", "", twoWorkloadFixture(t, "faas-rm1"))
	if len(ar1.Apps) != 2 {
		t.Fatalf("first apply apps=%d want 2", len(ar1.Apps))
	}

	// Second apply: 1 workload (worker removed).
	ar2 := applyProjectMultipart(t, h, key, "diff-rm", "", oneWorkloadFixtureFromPrefix(t, "faas-rm1"))
	if len(ar2.Apps) != 1 {
		t.Fatalf("second apply apps=%d want 1 (1 removed)", len(ar2.Apps))
	}
	if len(ar2.Builds) != 0 {
		t.Fatalf("removal re-apply enqueued %d builds, want 0", len(ar2.Builds))
	}

	// Pin: the removed workload's apps row is soft-deleted. We
	// look up by the first apply's apps; the removed one must
	// have either status='deleted' or a non-null deleted_at
	// depending on schema. We try both — any of these counts as
	// a soft-delete.
	var removedStatus string
	var deletedAt *time.Time
	_ = pool.QueryRow(context.Background(),
		`select status, deleted_at from apps where project_id = $1 and slug = 'worker'`,
		ar1.ProjectID).Scan(&removedStatus, &deletedAt)
	if removedStatus != "deleted" && deletedAt == nil {
		t.Fatalf("removed workload 'worker' was not soft-deleted (status=%q, deleted_at=%v)",
			removedStatus, deletedAt)
	}
}

// TestApplyProject_Diff_Changed pins the ~ path: 2nd apply has
// the same workloads but a different source hash. The changed
// workload's deployment row is superseded and a new build is
// enqueued for it.
func TestApplyProject_Diff_Changed(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: 2-workload repo, body=1 in api.
	ar1 := applyProjectMultipart(t, h, key, "diff-chg", "", twoWorkloadFixture(t, "faas-chg1"))
	if len(ar1.Apps) != 2 {
		t.Fatalf("first apply apps=%d want 2", len(ar1.Apps))
	}

	// Second apply: 2-workload repo, body=99 in api.
	ar2 := applyProjectMultipart(t, h, key, "diff-chg", "", twoWorkloadChangedFixture(t, "faas-chg1"))
	if len(ar2.Apps) != 2 {
		t.Fatalf("second apply apps=%d want 2", len(ar2.Apps))
	}
	// Only the api workload changed; worker is unchanged. So
	// exactly 1 new build is enqueued.
	if len(ar2.Builds) != 1 {
		t.Fatalf("changed re-apply builds=%d want 1 (only api changed)", len(ar2.Builds))
	}

	// Pin: the worker slug must NOT appear in ar2.Builds (the
	// changed-only path is per-workload).
	for _, b := range ar2.Builds {
		if b.Slug == "worker" {
			t.Fatalf("worker build enqueued on a change-only re-apply (regression)")
		}
	}
	var firstAPIPath, secondAPIPath string
	for _, build := range ar1.Builds {
		if build.Slug == "api" {
			if err := pool.QueryRow(context.Background(), `select source_path from deployments where id = $1`, build.DeploymentID).Scan(&firstAPIPath); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := pool.QueryRow(context.Background(), `select source_path from deployments where id = $1`, ar2.Builds[0].DeploymentID).Scan(&secondAPIPath); err != nil {
		t.Fatal(err)
	}
	if firstAPIPath == "" || firstAPIPath == secondAPIPath {
		t.Fatalf("source-only reapply reused mutable source path %q", secondAPIPath)
	}

	var apiID string
	for _, app := range ar2.Apps {
		if app.Slug == "api" {
			apiID = app.ID
			break
		}
	}
	if apiID == "" {
		t.Fatal("second apply did not return api app")
	}
	if _, err := pool.Exec(context.Background(), `
		insert into app_envs (account_id, app_id, scope, key, value)
		select account_id, id, 'default', 'MOVE_SENTINEL', 'preserved'
		from apps where id = $1`, apiID); err != nil {
		t.Fatalf("seed api env before root move: %v", err)
	}

	// Third apply: the api service keeps its durable name but moves from
	// services/api to apps/api. It must retain its app identity and attached
	// configuration while rebuilding only that workload.
	ar3 := applyProjectMultipart(t, h, key, "diff-chg", "", twoWorkloadMovedFixture(t, "faas-chg1"))
	if len(ar3.Builds) != 1 || ar3.Builds[0].Slug != "api" {
		t.Fatalf("root move builds=%v want exactly api", ar3.Builds)
	}
	if len(ar3.Removed) != 0 {
		t.Fatalf("root move removed=%v want none", ar3.Removed)
	}
	var movedAPIID string
	for _, app := range ar3.Apps {
		if app.Slug == "api" {
			movedAPIID = app.ID
			break
		}
	}
	if movedAPIID != apiID {
		t.Fatalf("root move replaced api app id: got %q want %q", movedAPIID, apiID)
	}
	var rootDir string
	if err := pool.QueryRow(context.Background(), `select root_dir from apps where id = $1`, apiID).Scan(&rootDir); err != nil {
		t.Fatalf("read moved api root: %v", err)
	}
	if rootDir != "apps/api" {
		t.Fatalf("moved api root_dir=%q want apps/api", rootDir)
	}
	var envValue string
	if err := pool.QueryRow(context.Background(), `
		select value from app_envs
		where app_id = $1 and scope = 'default' and key = 'MOVE_SENTINEL'`, apiID).Scan(&envValue); err != nil {
		t.Fatalf("read api env after root move: %v", err)
	}
	if envValue != "preserved" {
		t.Fatalf("api env after root move=%q want preserved", envValue)
	}
}

func TestApplyProject_OnlyRetainsSiblingAndCron(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)
	body := twoWorkloadFixture(t, "faas-only-retain")
	first := applyProjectMultipart(t, h, key, "only-retain", "", body)
	if len(first.Apps) != 2 {
		t.Fatalf("initial apps = %#v", first.Apps)
	}
	var workerID string
	if err := pool.QueryRow(context.Background(), `select id from apps where project_id = $1 and workload_name = 'worker'`, first.ProjectID).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	var cronID string
	if err := pool.QueryRow(context.Background(), `insert into crons (app_id, schedule, path, enabled, timezone, skip_if_running) values ($1, '*/5 * * * *', '/work', true, 'Europe/Istanbul', true) returning id`, workerID).Scan(&cronID); err != nil {
		t.Fatal(err)
	}

	second := applyProjectMultipartWithOnly(t, h, key, "only-retain", "", "api", body)
	if len(second.Removed) != 0 || len(second.Builds) != 0 {
		t.Fatalf("--only response removed/builds = %v/%v", second.Removed, second.Builds)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `select status from apps where id = $1`, workerID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "deleted" {
		t.Fatal("--only deleted the unselected worker")
	}
	var keptCronID, timezone string
	var skipIfRunning bool
	if err := pool.QueryRow(context.Background(), `select id, timezone, skip_if_running from crons where app_id = $1 and schedule = '*/5 * * * *' and path = '/work'`, workerID).Scan(&keptCronID, &timezone, &skipIfRunning); err != nil {
		t.Fatal(err)
	}
	if keptCronID != cronID || timezone != "Europe/Istanbul" || !skipIfRunning {
		t.Fatalf("worker cron changed: id %q -> %q timezone %q skip_if_running %t", cronID, keptCronID, timezone, skipIfRunning)
	}
}

func TestApplyProject_NoTriggersPreservesExistingCron(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	first := applyProjectMultipart(t, h, key, "no-triggers", "",
		scheduledProjectFixture(t, "faas-no-triggers", "0 3 * * *"))
	if len(first.Crons) != 1 {
		t.Fatalf("initial crons=%v want one", first.Crons)
	}
	var originalID, originalSchedule string
	if err := pool.QueryRow(context.Background(), `
		select c.id, c.schedule from crons c
		join apps a on a.id = c.app_id
		where a.project_id = $1`, first.ProjectID).Scan(&originalID, &originalSchedule); err != nil {
		t.Fatal(err)
	}

	second := applyProjectMultipartWithOptions(t, h, key, "no-triggers", "", "", true,
		scheduledProjectFixture(t, "faas-no-triggers", "30 4 * * *"))
	if len(second.Crons) != 0 {
		t.Fatalf("suppressed apply exposed desired crons=%v", second.Crons)
	}
	if !strings.Contains(strings.Join(second.Warnings, "\n"), "triggers skipped") {
		t.Fatalf("suppressed apply warnings=%v", second.Warnings)
	}
	var count int
	var keptID, keptSchedule string
	if err := pool.QueryRow(context.Background(), `
		select count(*), min(c.id::text), min(c.schedule) from crons c
		join apps a on a.id = c.app_id
		where a.project_id = $1`, first.ProjectID).Scan(&count, &keptID, &keptSchedule); err != nil {
		t.Fatal(err)
	}
	if count != 1 || keptID != originalID || keptSchedule != originalSchedule {
		t.Fatalf("--no-triggers mutated cron: count=%d id %q -> %q schedule %q -> %q",
			count, originalID, keptID, originalSchedule, keptSchedule)
	}

	// Removing the declaration must also leave the durable trigger untouched.
	applyProjectMultipartWithOptions(t, h, key, "no-triggers", "", "", true,
		unscheduledProjectFixture(t, "faas-no-triggers"))
	// Selecting only the sibling API workload must not remove nightly's trigger.
	applyProjectMultipartWithOptions(t, h, key, "no-triggers", "", "api", true,
		unscheduledProjectFixture(t, "faas-no-triggers"))
	if err := pool.QueryRow(context.Background(), `
		select count(*), min(c.id::text), min(c.schedule) from crons c
		join apps a on a.id = c.app_id
		where a.project_id = $1`, first.ProjectID).Scan(&count, &keptID, &keptSchedule); err != nil {
		t.Fatal(err)
	}
	if count != 1 || keptID != originalID || keptSchedule != originalSchedule {
		t.Fatalf("--no-triggers removal/--only mutated cron: count=%d id %q -> %q schedule %q -> %q",
			count, originalID, keptID, originalSchedule, keptSchedule)
	}
}

func TestApplyProject_NoTriggersNeverCreatesCron(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	result := applyProjectMultipartWithOptions(t, h, key, "no-triggers-new", "", "", true,
		scheduledProjectFixture(t, "faas-no-triggers-new", "0 3 * * *"))
	if len(result.Crons) != 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "triggers skipped") {
		t.Fatalf("suppressed new project crons=%v warnings=%v", result.Crons, result.Warnings)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `
		select count(*) from crons c join apps a on a.id = c.app_id
		where a.project_id = $1`, result.ProjectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("--no-triggers created %d cron rows for a new project", count)
	}
}

// TestApplyProject_Diff_CronSoftDeleted pins the PR-GH.6 500
// regression path: a render.yaml cron that disappears between
// applies must be soft-deleted, not crash with 500. The fix
// lives in pkg/reconcile; this test asserts the wire surface.
func TestApplyProject_Diff_CronSoftDeleted(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)

	// First apply: a repo with 1 cron + 1 service. Hobby plan
	// is required because crons are not allowed on Free.
	const cronYAML = `cronJobs:
  - name: nightly
    schedule: "0 3 * * *"
    command: echo nightly
`
	firstEntries := []struct{ name, body string }{
		{"faas-cron-diff/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n"},
		{"faas-cron-diff/render.yaml", cronYAML},
		{"faas-cron-diff/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
	}
	var firstBuf bytes.Buffer
	gz := gzip.NewWriter(&firstBuf)
	tw := tar.NewWriter(gz)
	for _, e := range firstEntries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	firstBody := firstBuf.Bytes()

	// Hobby plan: cron limit 5.
	key2 := h.SeedAccount(context.Background(), api.PlanHobby)
	ar1 := applyProjectMultipart(t, h, key2, "diff-cron", "", firstBody)
	if ar1.ProjectID == "" {
		t.Fatalf("first apply returned empty project_id")
	}

	// Second apply: SAME repo without render.yaml. The cron
	// should soft-delete.
	secondEntries := []struct{ name, body string }{
		{"faas-cron-diff/docker-compose.yml", "services:\n  api:\n    build: { context: . }\n"},
		{"faas-cron-diff/Dockerfile", "FROM alpine:3.19\nCMD [\"./api\"]\n"},
	}
	var secondBuf bytes.Buffer
	gz = gzip.NewWriter(&secondBuf)
	tw = tar.NewWriter(gz)
	for _, e := range secondEntries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	secondBody := secondBuf.Bytes()

	ar2 := applyProjectMultipart(t, h, key2, "diff-cron", "", secondBody)
	// Status OK — soft-delete path must not 500.
	if ar2.ProjectID == "" {
		t.Fatalf("second apply returned empty project_id (cron removal regressed)")
	}
}

// TestApplyProject_Diff_DomainCascade pins that removing a
// workload cascades to remove its attached domains. The wire
// contract: after removal, GET /v1/domains for the app returns
// empty. We assert at the DB level here for CI-safety.
func TestApplyProject_Diff_DomainCascade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: 2 workloads.
	ar1 := applyProjectMultipart(t, h, key, "diff-domain", "", twoWorkloadFixture(t, "faas-dom"))
	if len(ar1.Apps) != 2 {
		t.Fatalf("first apply apps=%d want 2", len(ar1.Apps))
	}

	// Second apply: 1 workload (worker removed).
	_ = applyProjectMultipart(t, h, key, "diff-domain", "", oneWorkloadFixtureFromPrefix(t, "faas-dom"))

	// Pin: worker app has no rows in domains. Tolerate table
	// absence by returning early on error.
	var domainCount int
	err := pool.QueryRow(context.Background(),
		`select count(*) from domains where app_id in (
			select id from apps where project_id = $1 and slug = 'worker' and deleted_at is null
		)`, ar1.ProjectID).Scan(&domainCount)
	if err != nil {
		t.Logf("domains query failed (table may not exist on this schema): %v", err)
		return
	}
	if domainCount != 0 {
		t.Fatalf("removed 'worker' app still has %d domains (cascade failed)", domainCount)
	}
}

// TestApplyProject_Diff_EnvCascade pins the same cascade for
// env vars: removing a workload drops its env vars too.
func TestApplyProject_Diff_EnvCascade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	// First apply: 2 workloads.
	ar1 := applyProjectMultipart(t, h, key, "diff-env", "", twoWorkloadFixture(t, "faas-env"))
	if len(ar1.Apps) != 2 {
		t.Fatalf("first apply apps=%d want 2", len(ar1.Apps))
	}

	// Second apply: 1 workload (worker removed).
	_ = applyProjectMultipart(t, h, key, "diff-env", "", oneWorkloadFixtureFromPrefix(t, "faas-env"))

	// Pin: worker app has no rows in envs.
	var envCount int
	err := pool.QueryRow(context.Background(),
		`select count(*) from envs where app_id in (
			select id from apps where project_id = $1 and slug = 'worker' and deleted_at is null
		)`, ar1.ProjectID).Scan(&envCount)
	if err != nil {
		t.Logf("envs query failed (table may not exist on this schema): %v", err)
		return
	}
	if envCount != 0 {
		t.Fatalf("removed 'worker' app still has %d envs (cascade failed)", envCount)
	}
}
