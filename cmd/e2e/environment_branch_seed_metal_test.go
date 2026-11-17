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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnvironmentBranchSeedTwinMetal proves ADR-20261031193635 slice 6b on a
// real guest: a rehearsal twin seeded from a simulated branch boots the
// branch's build, with no source build, and serves the branch's values.
// The run's containment and simulation then follow the twin's own app, as
// for any rehearsal member (covered by the rehearsal native acceptance).
func TestEnvironmentBranchSeedTwinMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping environment branch acceptance")
	}
	t.Setenv(api.EnvironmentBranchContainmentEnv, "1")
	t.Setenv("FAAS_REHEARSAL_NETWORK_CONTAINMENT", "1")
	t.Setenv("FAAS_REHEARSAL_ACTION_SIMULATION", "1")
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
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "environment-branch-seed")
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
	label := "seed" + randHexSuffix()
	key := h.SeedAccount(ctx, api.PlanPro, label)
	account, err := store.AccountByEmail(ctx, "e2e+pro+"+label+"@test.example")
	if err != nil {
		t.Fatalf("load seeded account: %v", err)
	}
	releaseEnvironmentBranches(t, store)

	slug := "seed-web-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "seeded-" + randHexSuffix(),
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

	raw, status := doReq(t, h, key, http.MethodPost, "/v1/projects/"+project.Slug+"/environments/production/branches",
		api.CreateProjectEnvironmentBranchRequest{Slug: "sim-1", Egress: api.EnvironmentBranchEgressSimulated},
		map[string]string{"Idempotency-Key": "simulated-" + randHexSuffix()})
	if status != http.StatusCreated {
		t.Fatalf("create simulated branch: status=%d body=%s", status, raw)
	}
	var created api.ProjectEnvironmentResponse
	if err := json.Unmarshal(raw, &created); err != nil || created.Branch == nil || created.Branch.Egress != api.EnvironmentBranchEgressSimulated || len(created.Branch.Workloads) != 1 {
		t.Fatalf("simulated branch response: %v (%s)", err, raw)
	}
	waitDeploymentLive(t, pool, created.Branch.Workloads[0].DeploymentID)
	if err := store.UpsertAppEnvInScope(ctx, account.ID, appID, "sim-1", "APP_COLOR", "twin-of-sim-1"); err != nil {
		t.Fatalf("branch env: %v", err)
	}

	// The rehearsal run's developer session, then the twin seeded into it.
	run := strings.Repeat("e", 16) + randHexSuffix()[:16]
	raw, status = doReq(t, h, key, http.MethodPut, "/v1/dev/sessions/twin-web", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("dev session: status=%d body=%s", status, raw)
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatalf("decode dev session: %v", err)
	}
	raw, status = doReq(t, h, key, http.MethodPost, "/v1/dev/sessions/twin-web/seed", api.SeedDevSessionFromEnvironmentBranchRequest{
		WorkspaceID: run, Project: project.Slug, Environment: "sim-1", Workload: "web",
	}, nil)
	if status != http.StatusAccepted {
		t.Fatalf("seed: status=%d body=%s", status, raw)
	}
	var seeded api.SeedDevSessionFromEnvironmentBranchResponse
	if err := json.Unmarshal(raw, &seeded); err != nil || seeded.DeploymentID == "" {
		t.Fatalf("seed response: %v (%s)", err, raw)
	}
	waitDeploymentLive(t, pool, seeded.DeploymentID)
	var builds int
	if err := pool.QueryRow(ctx, `select count(*) from builds where deployment_id = $1`, seeded.DeploymentID).Scan(&builds); err == nil && builds != 0 {
		t.Fatalf("the twin built source: %d builds", builds)
	}
	var twinKey, branchKey string
	if err := pool.QueryRow(ctx, `select rootfs_key from deployments where id = $1`, seeded.DeploymentID).Scan(&twinKey); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select rootfs_key from deployments where id = $1`, created.Branch.Workloads[0].DeploymentID).Scan(&branchKey); err != nil {
		t.Fatal(err)
	}
	if twinKey == "" || twinKey != branchKey {
		t.Fatalf("twin rootfs %q, branch rootfs %q: the twin must reuse the branch's build", twinKey, branchKey)
	}

	client := h.HTTPClient()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		body, _, status := branchGet(t, client, gatewayAppURL(h, session.App.Slug), session.App.Slug+".apps.test.example", key)
		if status == http.StatusOK && strings.HasPrefix(strings.TrimSpace(string(body)), "color=twin-of-sim-1") {
			t.Logf("twin %s served %q", session.App.Slug, strings.TrimSpace(string(body)))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("twin request: status=%d body=%q, want the branch's value", status, body)
		}
		time.Sleep(2 * time.Second)
	}
}
