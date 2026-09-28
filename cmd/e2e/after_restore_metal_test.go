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

// TestAfterRestoreMetal proves the complete application callback path on a
// real guest: the callback gates a successful restore, while a rejection or
// timeout rejects the restored VM and serves the request from a cold boot.
// One source deployment is reused; each arm request changes the in-memory
// state that the next snapshot captures.
func TestAfterRestoreMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping after_restore acceptance")
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
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "after-restore")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "after-restore-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		AfterRestore: &api.AfterRestoreHook{Path: "/internal/after-restore", TimeoutMS: 1000},
	}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	buildBody, status := postMultipartDeployment(t, h, key, slug, afterRestoreNodeFixture(t), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	depID, _ := parseQueuedDeployment(t, buildBody)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("source deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("snapshot prime did not park: %v", err)
	}
	store := state.NewPgStore(pool)
	snapshotID := waitAfterRestoreSnapshot(t, store, depID, "")
	url := gatewayAppURL(h, slug)
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	host := slug + ".apps.test.example"

	wake := func(t *testing.T, wantMethod, wantBody string) {
		t.Helper()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, host, 30*time.Second)
		if status != http.StatusOK || strings.TrimSpace(string(body)) != wantBody {
			t.Fatalf("wake: status=%d body=%q, want %q", status, body, wantBody)
		}
		if wakeID == "" {
			t.Fatal("wake response missing x-faas-wake-id")
		}
		wakeCtx, wakeCancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer wakeCancel()
		if _, err := e2etest.WaitForWakeMethod(wakeCtx, t, pool, wakeID, wantMethod, 10*time.Second); err != nil {
			t.Fatalf("wake %s: %v", wakeID, err)
		}
	}

	// The first restore must run the callback before the request reaches
	// the app. A lost callback or a callback after the ACK returns restored=0.
	wake(t, "restore", "restored=1 hooks=1")

	for _, mode := range []string{"reject", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			body, status := doGetWithHost(t, client, url+"arm/"+mode, host, 10*time.Second)
			if status != http.StatusOK || strings.TrimSpace(string(body)) != "armed="+mode {
				t.Fatalf("arm %s: status=%d body=%q", mode, status, body)
			}
			// Retire any eligible capture so Park must snapshot the newly
			// armed process. A cold-boot fallback may have re-primed since
			// the previous iteration. This is test setup, not product behavior.
			previousID := snapshotID
			latest, err := store.LatestSnapshotForTier(t.Context(), depID, state.SnapshotTierInit)
			if err == nil {
				previousID = latest.ID
				if err := store.MarkSnapshotStale(t.Context(), latest.ID); err != nil {
					t.Fatalf("retire previous snapshot: %v", err)
				}
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("read previous snapshot: %v", err)
			}
			if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
				t.Fatalf("park: status=%d body=%s", status, body)
			}
			snapshotID = waitAfterRestoreSnapshot(t, store, depID, previousID)
			ackBefore := strings.Count(h.VmmdLogs(), "application after_restore failed (ack=13)")
			wake(t, "cold_boot", "restored=0 hooks=0")
			if got := strings.Count(h.VmmdLogs(), "application after_restore failed (ack=13)"); got <= ackBefore {
				t.Fatalf("%s restore cold-booted without vmmd observing ACK 13", mode)
			}
			var stale bool
			if err := pool.QueryRow(t.Context(), `select stale from snapshots where id = $1`, snapshotID).Scan(&stale); err != nil {
				t.Fatalf("read failed snapshot: %v", err)
			}
			if !stale {
				t.Fatalf("failed %s snapshot %s remained eligible for restore", mode, snapshotID)
			}
			// Cold boot starts a fresh process; the next loop iteration
			// arms that process and captures it independently.
		})
	}
}

func waitAfterRestoreSnapshot(t *testing.T, store *state.PgStore, depID, previousID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		snap, err := store.LatestSnapshotForTier(ctx, depID, state.SnapshotTierInit)
		if err == nil && snap.ID != previousID {
			return snap.ID
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("latest init snapshot: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no new init snapshot after %s: %v", previousID, ctx.Err())
		case <-ticker.C:
		}
	}
}

// The handler changes process state only when the local, marked lifecycle
// request arrives. Cold boot starts with zero callbacks. The armed mode is
// frozen into the next snapshot, so its restore deterministically rejects or
// exceeds the one-second callback deadline.
func afterRestoreNodeFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"after-restore-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
let mode = 'ok';
let hooks = 0;
http.createServer((req, res) => {
  if (req.url === '/internal/after-restore') {
    if (req.method !== 'POST' || req.headers['x-faas-after-restore'] !== '1' ||
        !['127.0.0.1', '::ffff:127.0.0.1'].includes(req.socket.remoteAddress)) {
      res.writeHead(403); res.end(); return;
    }
    if (mode === 'reject') { res.writeHead(503); res.end(); return; }
    if (mode === 'timeout') { setTimeout(() => { res.writeHead(204); res.end(); }, 1500); return; }
    // Keep the callback outstanding long enough to catch a premature
    // readiness ACK that races the application callback.
    setTimeout(() => { hooks++; res.writeHead(204); res.end(); }, 200);
    return;
  }
  if (req.url.startsWith('/arm/')) {
    mode = req.url.slice('/arm/'.length);
    res.writeHead(200); res.end('armed=' + mode); return;
  }
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  res.writeHead(200);
  res.end('restored=' + (hooks > 0 ? '1' : '0') + ' hooks=' + hooks);
}).listen(8080, '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
