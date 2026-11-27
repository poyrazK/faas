//go:build metal

// dev_prime_serves_metal_test.go — ADR-20261122200000 on a real Firecracker
// stack: with dev_prime_serves_enabled on, a developer deployment goes live
// from its RUNNING prime without a snapshot, serves, and gets its first
// snapshot only once it parks.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

func TestDevPrimeServesMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal developer prime test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	// The setting is in place before schedd starts; its watcher applies it at
	// the initial reconcile.
	if _, err := pool.Exec(ctx, `insert into runtime_config_entries (id, config_key, scope, scope_id, desired_value, apply_mode, status)
		values ($1, $2, 'global', '', 'true'::jsonb, 'hot', 'pending')
		on conflict (config_key, scope, scope_id) do update set desired_value = excluded.desired_value`,
		uuid.NewString(), runtimeconfig.KeyDevPrimeServes); err != nil {
		t.Fatalf("enable %s: %v", runtimeconfig.KeyDevPrimeServes, err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "dev prime base")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All)
	defer h.DumpLogs(t)
	key := h.SeedAccount(ctx, api.PlanPro)

	sessionBody, status := doReq(t, h, key, http.MethodPut, "/v1/dev/sessions/devprime", api.UpsertDevSessionRequest{WorkspaceID: devLoopWorkspace})
	if status != http.StatusCreated {
		t.Fatalf("dev session: status %d body %s", status, sessionBody)
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(sessionBody, &session); err != nil {
		t.Fatal(err)
	}
	slug := session.App.Slug
	t.Cleanup(func() {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Errorf("cleanup park: status %d", status)
		}
	})
	host := slug + ".apps.test.example"
	falsy := false
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{RequireAuthn: &falsy,
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}); status != http.StatusOK {
		t.Fatalf("open app auth: status %d", status)
	}

	first := postDevSource(t, h, key, slug, devLoopFixture(t, "dev prime v1"))
	countSnapshots := func(t *testing.T) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from snapshots where deployment_id = $1`, first.ID).Scan(&n); err != nil {
			t.Fatalf("count snapshots: %v", err)
		}
		return n
	}
	var primeInstance string
	t.Run("live-from-running-prime", func(t *testing.T) {
		wctx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(wctx, t, pool, first.ID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("first sync did not go live: %v", err)
		}
		if err := pool.QueryRow(ctx, `select id::text from instances where deployment_id = $1 and state = 'running'`, first.ID).Scan(&primeInstance); err != nil {
			t.Fatalf("no RUNNING prime for the live deployment: %v", err)
		}
		if n := countSnapshots(t); n != 0 {
			t.Fatalf("snapshots before the first park = %d; want 0 (the prime serves without one)", n)
		}
		if got := devLoopBody(t, h, host); got != "dev prime v1" {
			t.Fatalf("body = %q, want v1", got)
		}
		var still string
		if err := pool.QueryRow(ctx, `select id::text from instances where deployment_id = $1 and state = 'running'`, first.ID).Scan(&still); err != nil || still != primeInstance {
			t.Fatalf("request served by %q (%v); want the prime %s", still, err, primeInstance)
		}
	})
	t.Run("first-idle-park-captures", func(t *testing.T) {
		if primeInstance == "" {
			t.Skip("deployment did not go live from its prime")
		}
		// The reaper's idle park, not a manual park: a manual park leaves the
		// app inactive, and clean recapture (the default) only re-primes
		// active apps. On an idle park it boots and captures a fresh
		// instance in the background (ADR-20261115113000).
		setAppIdleTimeout(t, h, key, slug, api.IdleTimeoutFloorSeconds)
		deadline := time.Now().Add(4 * time.Minute)
		for countSnapshots(t) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("no snapshot 4 min after the prime went idle")
			}
			time.Sleep(2 * time.Second)
		}
	})
	t.Run("wake-after-park", func(t *testing.T) {
		if got := devLoopBody(t, h, host); got != "dev prime v1" {
			t.Fatalf("body after park = %q, want v1", got)
		}
	})
}
