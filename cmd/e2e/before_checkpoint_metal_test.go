//go:build metal

package e2e_test

import (
	"context"
	"errors"
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

// A real Firecracker guest must run the callback before an init snapshot and
// must not publish a snapshot when the callback rejects the capture.
func TestBeforeCheckpointMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping before_checkpoint acceptance")
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
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "before-checkpoint")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "before-checkpoint-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		BeforeCheckpoint: &api.BeforeCheckpointHook{Path: "/internal/checkpoint", TimeoutMS: 1000},
	}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	buildBody, status := postMultipartDeployment(t, h, key, slug, beforeCheckpointNodeFixture(t), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	depID, _ := parseQueuedDeployment(t, buildBody)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("prime did not park: %v", err)
	}
	store := state.NewPgStore(pool)
	snapID := waitAfterRestoreSnapshot(t, store, depID, "")
	if _, err := store.LatestSnapshotForTier(ctx, depID, state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("warm snapshot was captured: %v", err)
	}
	url := gatewayAppURL(h, slug)
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	host := slug + ".apps.test.example"
	body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, host, 30*time.Second)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != "checkpoints=1" {
		t.Fatalf("restore: status=%d body=%q", status, body)
	}
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, "restore", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if body, status := doGetWithHost(t, client, url+"arm/reject", host, 10*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != "armed" {
		t.Fatalf("arm: status=%d body=%q", status, body)
	}
	if err := store.MarkSnapshotStale(ctx, snapID); err != nil {
		t.Fatal(err)
	}
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
		t.Fatalf("park: status=%d body=%s", status, body)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateStopped, 30*time.Second); err != nil {
		t.Fatalf("failed capture did not stop guest: %v", err)
	}
	if _, err := store.LatestSnapshotForTier(ctx, depID, state.SnapshotTierInit); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed callback published an init snapshot: %v", err)
	}
}

func beforeCheckpointNodeFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"before-checkpoint-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
let checkpoints = 0;
let mode = 'ok';
http.createServer((req, res) => {
  if (req.url === '/internal/checkpoint') {
    if (req.method !== 'POST' || req.headers['x-faas-before-checkpoint'] !== '1' ||
        !['127.0.0.1', '::ffff:127.0.0.1'].includes(req.socket.remoteAddress)) {
      res.writeHead(403); res.end(); return;
    }
    if (mode === 'reject') { res.writeHead(503); res.end(); return; }
    checkpoints++; res.writeHead(204); res.end(); return;
  }
  if (req.url === '/arm/reject') { mode = 'reject'; res.writeHead(200); res.end('armed'); return; }
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  res.writeHead(200); res.end('checkpoints=' + checkpoints);
}).listen(8080, '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json": packageJSON, "index.js": indexJS, ".faas-fixture": "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
