//go:build !no_pg

// adr: 438
package state_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestOrdinaryCapacityPostgresReplayAndDowngrade(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	fx := conformance.Seed(t, store)
	existing, err := store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range existing {
		if err := store.SetComputeNodeActive(ctx, node.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	region := "eu-fsn"
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	var fleet []state.ComputeNode
	for range 2 {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), Region: &region, TargetURL: "unix:///tmp/ordinary-replay.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 2080, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, node)
	}
	conformance.KeepNodesHeartbeating(t, store, fleet)
	service, err := store.CreateApp(ctx, state.App{AccountID: fx.Account.ID, Slug: "ordinary-replay", ComputeRegion: region, RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 5, Desired: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: service.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage}); err != nil {
		t.Fatal(err)
	}
	before, err := store.SetServiceCapacityProtection(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/20261002200131931_service_capacity_ordinary_placement.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(strings.Split(string(data), "-- +goose Up\n")[1], "-- +goose Down\n")[0]
	down := strings.Split(string(data), "-- +goose Down\n")[1]
	for _, body := range []string{up, down} {
		exerciseRegionalMigrationPolicyLock(t, pool, body)
		restarted := state.NewPgStore(pool)
		after, err := restarted.ServiceCapacityProtection(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("metadata migration changed enabled certificate: before=%+v after=%+v %v", before, after, err)
		}
		hint, err := restarted.ServiceCapacityPlacement(ctx)
		if err != nil || hint.AllowsOrdinary(fleet[0].ID, 520, 1000, 2) != (body == up) {
			t.Fatalf("missing metadata did not fail ordinary placement closed: %+v %v", hint, err)
		}
	}
	// Down removes hints, but retains the independent atomic admission guard.
	if _, err := pool.Exec(ctx, `INSERT INTO instances(app_id,deployment_id,node_id,state,ram_mb) VALUES($1,$2,$3,'running',1024)`, fx.App.ID, fx.Deployment.ID, fleet[0].ID); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("downgrade discarded recovery guard: %v", err)
	}
	exerciseRegionalMigrationPolicyLock(t, pool, up)
	hint, err := store.ServiceCapacityPlacement(ctx)
	if err != nil || !hint.AllowsOrdinary(fleet[0].ID, 520, 1000, 2) {
		t.Fatalf("re-upgrade did not restore ordinary hints: %+v %v", hint, err)
	}
}
