//go:build !no_pg

// adr: 375
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// Two HTTP synthetic endpoints use independent pools/registries against the
// same real database. Forwarding is a fixture; this is not fleet/KVM acceptance.
func TestSyntheticTrafficSecurityPostgresMissedReleaseAndOutage(t *testing.T) {
	ctx := t.Context()
	firstPool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, firstPool); err != nil {
		t.Fatal(err)
	}
	secondPool, err := pgxpool.NewWithConfig(ctx, firstPool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer secondPool.Close()
	store := state.NewPgStore(secondPool)
	account, err := store.CreateAccount(ctx, "synth-security-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "synth-security-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:synthetic-security", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "synth-security-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	pools := []*pgxpool.Pool{firstPool, secondPool}
	registries := make([]*trafficrevocation.Registry, 2)
	servers := make([]*httptest.Server, 2)
	started := make(chan int, 4)
	for i, pool := range pools {
		registry := trafficrevocation.New(state.NewPGTrafficSecurityBackend(pool))
		registries[i] = registry
		defer registry.Close()
		adapter := &synthAdapter{store: state.NewPgStore(pool), trafficRevocations: registry,
			forward: func(gateway.Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/hold" {
						started <- i
						<-r.Context().Done()
					}
					_, _ = w.Write([]byte(`{"ok":true}`))
				})
			},
		}
		servers[i] = httptest.NewServer(gateway.NewSynthServer("", adapter, nil).Mux())
		defer servers[i].Close()
	}
	call := func(i int, path string) (int, error) {
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		body, err := json.Marshal(map[string]any{"invocation_id": uuid.NewString(), "account_id": account.ID, "app_id": app.ID, "source": state.InvocationAsyncInvoke,
			"instance_id": instance.ID, "node_id": node.ID, "deployment_id": dep.ID, "path": path})
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, servers[i].URL+"/v1/invocations:dispatch", bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, err
	}
	results := make(chan error, 2)
	hold := func(i int) {
		go func() {
			status, err := call(i, "/hold")
			if err == nil && status != http.StatusBadGateway {
				err = errors.New("revoked synthetic HTTP exchange returned success")
			}
			results <- err
		}()
	}
	waitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("synthetic forwarding did not start")
		}
	}
	waitResult := func() {
		t.Helper()
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("synthetic forwarding did not finish")
		}
	}
	hold(0)
	hold(1)
	waitStarted()
	waitStarted()
	// Deliberately omit notifications and refresh only after both transitions.
	if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	for _, registry := range registries {
		if err := registry.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	waitResult()
	waitResult()
	for i, registry := range registries {
		assertSyntheticSecurityReleased(t, registry)
		if status, err := call(i, "/fresh"); err != nil || status != http.StatusOK {
			t.Fatalf("fresh released generation: %d %v", status, err)
		}
	}
	hold(0)
	waitStarted()
	firstPool.Close()
	if err := registries[0].Refresh(ctx); !errors.Is(err, trafficrevocation.ErrUnavailable) {
		t.Fatalf("pool outage refresh: %v", err)
	}
	waitResult()
	assertSyntheticSecurityReleased(t, registries[0])
	if status, err := call(0, "/fresh"); err != nil || status != http.StatusBadGateway {
		t.Fatalf("unavailable endpoint admitted new delivery: %d %v", status, err)
	}
	if status, err := call(1, "/fresh"); err != nil || status != http.StatusOK {
		t.Fatalf("healthy peer delivery: %d %v", status, err)
	}
	assertSyntheticSecurityReleased(t, registries[1])
}
