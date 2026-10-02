//go:build !no_pg

// adr: 459
package state_test

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestServiceCapacityProjectionJITPostgres(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app := seedCapacityProjectionJIT(t, store)
	before, err := store.SetServiceCapacityProtection(ctx, true)
	if err != nil || before.State != "protected" {
		t.Fatalf("enable populated regional fixture: %+v %v", before, err)
	}
	assertCapacityProjectionJITScope(t, pool)
	// The optimization must preserve the regional admission boundary.
	manifest := app.Manifest
	target := *manifest.ServiceReplicas
	target.Desired = 5
	manifest.ServiceReplicas = &target
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("JIT setting weakened regional protection: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER FUNCTION service_capacity_snapshot_inputs() SET extra_float_digits=2`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/20261002212534134_service_capacity_projection_jit.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(strings.Split(string(data), "-- +goose Up\n")[1], "-- +goose Down\n")[0]
	down := strings.Split(string(data), "-- +goose Down\n")[1]
	for _, phase := range []struct {
		name, body string
		jitOff     bool
	}{{"replay", up, true}, {"down", down, false}, {"up", up, true}} {
		t.Run(phase.name, func(t *testing.T) {
			exerciseRegionalMigrationPolicyLock(t, pool, phase.body) //nolint:contextcheck // The helper derives a bounded context from this subtest instead of the fixture context.
			var settings []string
			if err := pool.QueryRow(ctx, `SELECT proconfig FROM pg_proc WHERE oid='service_capacity_snapshot_inputs()'::regprocedure`).Scan(&settings); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(settings, "jit=off") != phase.jitOff || !slices.Contains(settings, "extra_float_digits=2") {
				t.Fatalf("migration discarded function settings: %v", settings)
			}
			// Down inherits the caller again. Disable JIT in this observation
			// transaction to compare the certificate without its compiler cost.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `SET LOCAL jit=off`); err != nil {
				t.Fatal(err)
			}
			var after api.ServiceCapacityProtection
			if err := tx.QueryRow(ctx, `SELECT service_capacity_snapshot()`).Scan(&after); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("migration changed enabled certificate: before=%+v after=%+v %v", before, after, err)
			}
			if phase.jitOff {
				assertCapacityProjectionJITScope(t, pool) //nolint:contextcheck // This assertion derives its deadline from the subtest's context.
			}
		})
	}
}

func assertCapacityProjectionJITScope(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SET LOCAL jit=off`); err != nil {
		t.Fatal(err)
	}
	var expected []byte
	if err := tx.QueryRow(ctx, `SELECT service_capacity_snapshot_inputs()`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL jit=on; SET LOCAL jit_above_cost=0; SET LOCAL jit_inline_above_cost=0; SET LOCAL jit_optimize_above_cost=0`); err != nil {
		t.Fatal(err)
	}
	// Force the planner's JIT path instead of relying on fixture statistics.
	// The function's local setting must avoid seconds of compiler work while
	// returning exactly the same projection. This is a generous regression
	// deadline, not a production throughput or latency threshold.
	queryCtx, queryCancel := context.WithTimeout(ctx, 5*time.Second)
	defer queryCancel()
	var actual []byte
	if err := tx.QueryRow(queryCtx, `SELECT service_capacity_snapshot_inputs()`).Scan(&actual); err != nil {
		t.Fatalf("bounded projection with caller JIT forced: %v", err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatal("JIT setting changed projection values")
	}
	var jit, cost string
	if err := tx.QueryRow(ctx, `SELECT current_setting('jit'),current_setting('jit_above_cost')`).Scan(&jit, &cost); err != nil || jit != "on" || cost != "0" {
		t.Fatalf("projection changed caller settings: jit=%s cost=%s %v", jit, cost, err)
	}
}

func seedCapacityProjectionJIT(t *testing.T, store state.Store) state.App {
	t.Helper()
	fx := conformance.Seed(t, store)
	nodes, err := store.ListAllComputeNodes(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if err := store.SetComputeNodeActive(fx.Ctx, node.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for _, region := range []string{"jit-selected", "jit-selected", "jit-foreign"} {
		if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		node, err := store.CreateComputeNode(fx.Ctx, state.ComputeNode{Name: uuid.NewString(), Region: &region, TargetURL: "unix:///tmp/jit-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 2080, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, node)
	}
	conformance.KeepNodesHeartbeating(t, store, fleet)
	app, err := store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "jit-capacity", ComputeRegion: "jit-selected", RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 20, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 20, Desired: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(fx.Ctx, app.ID, dep.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 128, fleet[2].ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return app
}
