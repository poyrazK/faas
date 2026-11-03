//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnvironmentBranchIdentityReloadMetal proves ADR-20261031193635 slice 5
// on a real guest. The first wake of an opted-in branch cold-boots and queues
// the parent's credential-free capture; schedd's worker makes it on a
// quarantined VM. A later branch's first wake restores that capture, and the
// after_restore hook reloads the branch's own identity from the tmpfs file and
// acknowledges its digest: the restored process serves the branch's value,
// never the parent's.
func TestEnvironmentBranchIdentityReloadMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping environment branch acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "environment-branch")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	store := state.NewPgStore(pool)
	label := "branch" + randHexSuffix()
	key := h.SeedAccount(ctx, api.PlanPro, label)
	account, err := store.AccountByEmail(ctx, "e2e+pro+"+label+"@test.example")
	if err != nil {
		t.Fatalf("load seeded account: %v", err)
	}
	releaseEnvironmentBranches(t, store)

	slug := "branch-web-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		AfterRestore: &api.AfterRestoreHook{Path: "/internal/after-restore", TimeoutMS: 3000, Identity: api.AfterRestoreIdentityReload},
	}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "branched-" + randHexSuffix(),
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Fixture setup: attach the API-created app to the project as a workload.
	if _, err := pool.Exec(ctx, `update apps set project_id = $1, workload_name = 'web' where id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app to project: %v", err)
	}
	if err := store.UpsertAppEnv(ctx, account.ID, appID, "APP_COLOR", "production"); err != nil {
		t.Fatalf("production env: %v", err)
	}

	buildBody, status := postMultipartDeployment(t, h, key, slug, environmentBranchNodeFixture(t), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	parentID, _ := parseQueuedDeployment(t, buildBody)
	deployCtx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, pool, parentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("parent deployment did not reach live: %v", err)
	}
	client := h.HTTPClient()
	url := gatewayAppURL(h, slug)

	// Branch one: its first wake cold-boots under its own identity and queues
	// the parent's credential-free capture.
	first := createEnvironmentBranch(t, h, key, project.Slug, "pr-1", appID)
	body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, environmentBranchHost(t, store, account.ID, project.ID, "pr-1", appID), 60*time.Second)
	if status != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(string(body)), "color=production reloads=0") {
		t.Fatalf("first branch wake: status=%d body=%q", status, body)
	}
	waitWakeMethod(t, pool, wakeID, "cold_boot")
	capture := waitEnvironmentBranchCapture(t, pool, parentID)
	var snapshotRows int
	if err := pool.QueryRow(ctx, `select count(*) from snapshots where id = $1`, capture).Scan(&snapshotRows); err != nil || snapshotRows != 0 {
		t.Fatalf("the credential-free capture reached the snapshots table: rows=%d err=%v", snapshotRows, err)
	}
	t.Logf("first branch %s cold-booted; capture %s ready", first, capture)

	// Branch two gets its own value, then wakes for the first time: it must
	// restore the capture and reload that value through after_restore.
	second := createEnvironmentBranch(t, h, key, project.Slug, "pr-2", appID)
	var scope string
	if err := pool.QueryRow(ctx, `select scope from deployments where id = $1`, second).Scan(&scope); err != nil {
		t.Fatalf("branch deployment scope: %v", err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, appID, scope, "APP_COLOR", "branch-two"); err != nil {
		t.Fatalf("branch env: %v", err)
	}
	body, wakeID, status = doGetWithHostCapturingWakeID(t, client, url, environmentBranchHost(t, store, account.ID, project.ID, "pr-2", appID), 60*time.Second)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != "color=branch-two reloads=1" {
		t.Fatalf("second branch wake: status=%d body=%q, want the branch's reloaded identity", status, body)
	}
	waitWakeMethod(t, pool, wakeID, "restore")
	if strings.Contains(h.VmmdLogs(), "restore identity not acknowledged") {
		t.Fatal("vmmd rejected the acknowledged identity")
	}
	var captureState string
	if err := pool.QueryRow(ctx, `select state from environment_branch_captures where id = $1`, capture).Scan(&captureState); err != nil || captureState != "ready" {
		t.Fatalf("capture after an acknowledged restore: state=%q err=%v", captureState, err)
	}
}

func releaseEnvironmentBranches(t *testing.T, store *state.PgStore) {
	t.Helper()
	row, err := store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: "environment_branches_enabled", Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot, ActorID: uuid.NewString(), Reason: "environment branch metal fixture"})
	if err != nil {
		t.Fatalf("release environment branches: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		receipts, err := store.ListRuntimeConfigApplyReceipts(t.Context(), []string{row.Key})
		if err == nil {
			for _, r := range receipts {
				if r.Daemon == "apid" && r.Status == state.RuntimeConfigReceiptApplied && r.Version >= row.Version {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("apid did not apply environment_branches_enabled")
}

// createEnvironmentBranch branches production and returns the branch's
// deployment of appID.
func createEnvironmentBranch(t *testing.T, h *e2etest.Harness, key, projectSlug, branch, appID string) string {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/projects/"+projectSlug+"/environments/production/branches",
		api.CreateProjectEnvironmentBranchRequest{Slug: branch}, map[string]string{"Idempotency-Key": "branch-" + branch + "-" + randHexSuffix()})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("create branch %s: status=%d body=%s", branch, status, raw)
	}
	var created api.ProjectEnvironmentBranchResponse
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode branch %s: %v (%s)", branch, err, raw)
	}
	for _, workload := range created.Workloads {
		if workload.DeploymentID != "" {
			return workload.DeploymentID
		}
	}
	t.Fatalf("branch %s published no workload for app %s: %s", branch, appID, raw)
	return ""
}

func environmentBranchHost(t *testing.T, store *state.PgStore, accountID, projectID, branch, appID string) string {
	t.Helper()
	environment, err := store.ProjectEnvironmentBySlug(t.Context(), accountID, projectID, branch)
	if err != nil {
		t.Fatalf("branch environment %s: %v", branch, err)
	}
	host := hostidentity.BuildEnvironmentHost(".apps.test.example", environment.ID, appID)
	if host == "" {
		t.Fatalf("no environment host for %s/%s", environment.ID, appID)
	}
	return host
}

func waitWakeMethod(t *testing.T, pool *pgxpool.Pool, wakeID, method string) {
	t.Helper()
	if wakeID == "" {
		t.Fatal("wake response missing x-faas-wake-id")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, method, 20*time.Second); err != nil {
		t.Fatalf("wake %s: %v", wakeID, err)
	}
}

// waitEnvironmentBranchCapture waits for the parent deployment's capture to
// become ready and returns its ID.
func waitEnvironmentBranchCapture(t *testing.T, pool *pgxpool.Pool, parentID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		var id, captureState, failure string
		err := pool.QueryRow(t.Context(), `select id::text, state, failure from environment_branch_captures where deployment_id = $1`, parentID).Scan(&id, &captureState, &failure)
		if err == nil && captureState == "ready" {
			return id
		}
		if err == nil && captureState == "failed" {
			t.Fatalf("credential-free capture failed: %s", failure)
		}
		time.Sleep(time.Second)
	}
	t.Fatal("credential-free capture never became ready")
	return ""
}

// The app reports the APP_COLOR it serves. Restored memory holds the capture
// boot's value until after_restore reloads the identity file and acknowledges
// its digest.
func environmentBranchNodeFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"environment-branch-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
const fs = require('fs');
let color = process.env.APP_COLOR || 'unset';
let reloads = 0;
http.createServer((req, res) => {
  if (req.url === '/internal/after-restore') {
    if (req.method !== 'POST' || req.headers['x-faas-after-restore'] !== '1') { res.writeHead(403); res.end(); return; }
    const file = req.headers['x-faas-identity-file'];
    const digest = req.headers['x-faas-identity-digest'];
    if (file && digest) {
      try {
        const doc = JSON.parse(fs.readFileSync(file, 'utf8'));
        color = (doc.env && doc.env.APP_COLOR) || 'missing';
        reloads++;
        res.writeHead(204, { 'X-Faas-Identity-Ack': digest }); res.end();
      } catch (e) { res.writeHead(500); res.end(); }
      return;
    }
    res.writeHead(204); res.end(); return;
  }
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  res.writeHead(200);
  res.end('color=' + color + ' reloads=' + reloads);
}).listen(Number(process.env.PORT || 8080), '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
