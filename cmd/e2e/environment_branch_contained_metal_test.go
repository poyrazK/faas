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

// TestEnvironmentBranchContainedEgressMetal proves ADR-20261031193635 slice 6a
// on a real guest. The same build serves production and a contained branch.
// Production's guest opens TCP to a public address; the contained branch's
// guest cannot, yet serves its own ingress and keeps the bridge resolver.
func TestEnvironmentBranchContainedEgressMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping environment branch acceptance")
	}
	t.Setenv(api.EnvironmentBranchContainmentEnv, "1")
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
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "environment-branch-contained")
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
	label := "contained" + randHexSuffix()
	key := h.SeedAccount(ctx, api.PlanPro, label)
	account, err := store.AccountByEmail(ctx, "e2e+pro+"+label+"@test.example")
	if err != nil {
		t.Fatalf("load seeded account: %v", err)
	}
	releaseEnvironmentBranches(t, store)

	slug := "contained-web-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "contained-" + randHexSuffix(),
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Fixture setup: attach the API-created app to the project as a workload.
	if _, err := pool.Exec(ctx, `update apps set project_id = $1, workload_name = 'web' where id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app to project: %v", err)
	}
	buildBody, status := postMultipartDeployment(t, h, key, slug, environmentBranchEgressProbeFixture(t), false, "")
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
	probeURL := gatewayAppURL(h, slug) + "probe"

	// Control: production's guest reaches the public address.
	body, _, status := branchGet(t, client, probeURL, slug+".apps.test.example", key)
	if status != http.StatusOK {
		t.Fatalf("production probe: status=%d body=%q", status, body)
	}
	if got := strings.TrimSpace(string(body)); got != "public=connected dns=ok" {
		t.Skipf("production guest has no public egress on this host (%q); containment cannot be told apart", got)
	}

	raw, status := doReq(t, h, key, http.MethodPost, "/v1/projects/"+project.Slug+"/environments/production/branches",
		api.CreateProjectEnvironmentBranchRequest{Slug: "pr-1", Egress: api.EnvironmentBranchEgressContained},
		map[string]string{"Idempotency-Key": "contained-" + randHexSuffix()})
	if status != http.StatusCreated {
		t.Fatalf("create contained branch: status=%d body=%s", status, raw)
	}
	var created api.ProjectEnvironmentResponse
	if err := json.Unmarshal(raw, &created); err != nil || created.Branch == nil || created.Branch.Egress != api.EnvironmentBranchEgressContained || len(created.Branch.Workloads) != 1 {
		t.Fatalf("contained branch response: %v (%s)", err, raw)
	}
	branchDeployment := created.Branch.Workloads[0].DeploymentID
	waitDeploymentLive(t, pool, branchDeployment)

	body, wakeID, status := branchGet(t, client, probeURL, environmentBranchHost(t, store, account.ID, project.ID, "pr-1", appID), key)
	if status != http.StatusOK {
		t.Fatalf("contained branch probe: status=%d body=%q", status, body)
	}
	if got := strings.TrimSpace(string(body)); got != "public=blocked dns=ok" {
		t.Fatalf("contained branch probe = %q, want public egress blocked and the resolver reachable", got)
	}
	waitWakeMethod(t, pool, wakeID, "cold_boot")
	if strings.Contains(h.VmmdLogs(), "environment branch containment authority is unavailable") {
		t.Fatal("vmmd ran without the containment authority")
	}
}

// The app reports whether it can open TCP to a public address and resolve a
// name through the platform resolver.
func environmentBranchEgressProbeFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"environment-branch-contained-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
const net = require('net');
const dns = require('dns');
function publicTCP() {
  return new Promise((resolve) => {
    const s = net.connect({ host: '1.1.1.1', port: 443 });
    const done = (v) => { s.destroy(); resolve(v); };
    s.setTimeout(3000, () => done('blocked'));
    s.on('connect', () => done('connected'));
    s.on('error', () => done('blocked'));
  });
}
function resolver() {
  return new Promise((resolve) => {
    dns.resolve4('one.one.one.one', (err) => resolve(err ? 'failed' : 'ok'));
  });
}
http.createServer(async (req, res) => {
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  if (req.url === '/probe') {
    const [pub, d] = await Promise.all([publicTCP(), resolver()]);
    res.writeHead(200); res.end('public=' + pub + ' dns=' + d); return;
  }
  res.writeHead(200); res.end('ok');
}).listen(Number(process.env.PORT || 8080), '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
