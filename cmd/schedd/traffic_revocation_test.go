// adr: 531
package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto/tls"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func TestRunInvocationSecurityStoreRefusesBeforeLifecycleStartup(t *testing.T) {
	for _, kind := range []string{"missing migration", "closed pool"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.Open(t)
			if kind == "closed pool" {
				pool.Close()
			}
			lifecycleStarted := false
			deps := runDeps{configPath: filepath.Join(t.TempDir(), "absent.toml"), capCheck: func() error { return nil },
				openDB: func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil }, migrate: func(context.Context, *pgxpool.Pool) error { return nil },
				detectFC: func(context.Context) (string, error) { lifecycleStarted = true; return "1.10.0", nil }}
			err := runWithDeps(t.Context(), discardLog(), deps)
			if err == nil || !strings.Contains(err.Error(), "verify invocation traffic security store") || lifecycleStarted {
				t.Fatalf("startup gate: %v lifecycle=%v", err, lifecycleStarted)
			}
		})
	}
}

// Runs the real daemon factory, drain, PostgreSQL store and HTTP producer. VMM
// and the receiving gateway are fixtures; this is not deployed/KVM acceptance.
func TestRunInvocationSecurityPublishesActualDrainHandoff(t *testing.T) {
	pool := migratedPool(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "sched-security-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "sched-security-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:sched-security", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "sched-security-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: acct.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/test", DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	dir := shortDir(t)
	synthPath := filepath.Join(dir, "synth.sock")
	listener, err := net.Listen("unix", synthPath)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 2)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			SecuritySnapshot string `json:"security_snapshot"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		received <- envelope.SecuritySnapshot
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching","result":{"ok":true}}`))
	})}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()
	cfgPath := filepath.Join(dir, "schedd.toml")
	cfg := "socket_path = \"" + filepath.Join(dir, "schedd.sock") + "\"\nowner_user = \"root\"\ngateway_synth_target = \"unix://" + synthPath + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	deps := runDeps{configPath: cfgPath, capCheck: func() error { return nil }, openDB: func(context.Context, string) (*pgxpool.Pool, error) { return pool, nil },
		migrate: func(context.Context, *pgxpool.Pool) error { return nil }, detectFC: func(context.Context) (string, error) { return "1.10.0", nil }, dialVMM: stubDialVMM, signPubPath: writeTestSignPub(t),
		listen: func(_ context.Context, target string, _ *tls.Config, _ string) (net.Listener, error) {
			return net.Listen("unix", strings.TrimPrefix(target, "unix://"))
		}}
	runCtx, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { finished <- runWithDeps(runCtx, discardLog(), deps) }()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("daemon shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	}()
	select {
	case value := <-received:
		states, err := trafficrevocation.DecodeSnapshot(value)
		if err != nil || len(states) != 3 {
			t.Fatalf("actual drain omitted security handoff: %q %v", value, err)
		}
		for _, scope := range []trafficrevocation.Scope{{Kind: "account", ID: acct.ID}, {Kind: "app", ID: app.ID}, {Kind: "deployment", ID: dep.ID}} {
			if _, ok := states[scope]; !ok {
				t.Fatalf("wrong admitted owner: %#v", states)
			}
		}
	case err := <-finished:
		finished <- err
		t.Fatalf("daemon exited before dispatch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("actual drain did not reach synthetic gateway")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row, err := store.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]bool
		if row.State == state.InvocationCompleted && json.Unmarshal(row.Result, &result) == nil && len(result) == 1 && result["ok"] {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual drain did not persist completion")
}
