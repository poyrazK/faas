//go:build !no_pg

// adr: 463
package state_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestRegionalServiceCapacityPostgresRawSQLReplayAndDowngrade(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	fx := conformance.Seed(t, store)
	nodes, err := store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := store.SetComputeNodeActive(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	fleet := map[string][]state.ComputeNode{}
	for _, region := range []string{"eu-fsn", "us-ash"} {
		if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		count := 3
		if region == "eu-fsn" {
			count = 2
		}
		for range count {
			n, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "capacity-" + uuid.NewString(), Region: &region, TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 2080, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive})
			if err != nil {
				t.Fatal(err)
			}
			fleet[region] = append(fleet[region], n)
		}
		conformance.KeepNodesHeartbeating(t, store, fleet[region])
	}
	apps := map[string]state.App{}
	deps := map[string]state.Deployment{}
	for _, region := range []string{"eu-fsn", "us-ash", ""} {
		app, err := store.CreateApp(ctx, state.App{AccountID: fx.Account.ID, Slug: "sql-capacity-" + uuid.NewString(), ComputeRegion: region, RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 20, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 20, Desired: 4}}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		apps[region] = app
		deps[region] = dep
	}
	if _, err := store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest=jsonb_set(manifest,'{service_replicas,desired}','5') WHERE id=$1`, apps["eu-fsn"].ID); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("raw intent borrowed foreign headroom: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(app_id,deployment_id,node_id,state,ram_mb,mode) VALUES($1,$2,$3,'running',512,'service')`, apps[""].ID, deps[""].ID, fleet["eu-fsn"][0].ID); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("raw legacy placement stole regional recovery: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(app_id,deployment_id,node_id,state,ram_mb,mode) VALUES($1,$2,$3,'running',512,'service')`, apps[""].ID, deps[""].ID, fleet["us-ash"][0].ID); err != nil {
		t.Fatalf("safe raw legacy placement: %v", err)
	}
	before, err := store.ServiceCapacityProtection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../migrations/20261002181905649_regional_service_capacity.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(strings.Split(string(data), "-- +goose Up\n")[1], "-- +goose Down\n")[0]
	down := strings.Split(string(data), "-- +goose Down\n")[1]
	exerciseRegionalMigrationPolicyLock(t, pool, up)
	restarted := state.NewPgStore(pool)
	after, err := restarted.ServiceCapacityProtection(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("replay/restart changed certificate: before=%+v after=%+v err=%v", before, after, err)
	}
	var definition string
	if err := pool.QueryRow(ctx, `SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid='compute_nodes'::regclass AND tgname='service_capacity_nodes_lock'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "UPDATE OF region,") {
		t.Fatalf("relabel not serialized: %s", definition)
	}
	if _, err := pool.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "disable service capacity protection") {
		t.Fatalf("downgrade discarded enabled regional certificate: %v", err)
	}
	after, err = restarted.ServiceCapacityProtection(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused downgrade mutated policy: %+v %v", after, err)
	}
	if _, err := store.SetServiceCapacityProtection(ctx, false); err != nil {
		t.Fatal(err)
	}
	exerciseRegionalMigrationPolicyLock(t, pool, down)
	after, err = restarted.ServiceCapacityProtection(ctx)
	if err != nil || after.Enabled || len(after.Regions) != 0 || after.ReservedReplicas != before.ReservedReplicas {
		t.Fatalf("legacy downgrade certificate: %+v %v", after, err)
	}
	var helpers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND proname IN ('service_capacity_snapshot_inputs','service_capacity_region_legacy_limit')`).Scan(&helpers); err != nil || helpers != 0 {
		t.Fatalf("downgrade helper cleanup: %d %v", helpers, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if after, err := store.SetServiceCapacityProtection(ctx, true); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("re-upgrade: %+v %v", after, err)
	}
}

// Observe PostgreSQL's actual blocking relationship before releasing an
// in-flight admission. Both DDL directions must take the policy boundary.
func exerciseRegionalMigrationPolicyLock(t *testing.T, pool *pgxpool.Pool, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `SELECT singleton FROM service_capacity_policy WHERE singleton FOR UPDATE`); err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		_ = transaction.Rollback(ctx)
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := connection.Exec(ctx, body); result <- err }()
	blocked, finished := false, false
	var migrationErr, observationErr error
	deadline := time.Now().Add(3 * time.Second)
	for !blocked && !finished && time.Now().Before(deadline) {
		observationErr = pool.QueryRow(ctx, `SELECT $1::integer=ANY(pg_blocking_pids($2::integer))`, transaction.Conn().PgConn().PID(), connection.Conn().PgConn().PID()).Scan(&blocked)
		if observationErr != nil {
			break
		}
		select {
		case migrationErr = <-result:
			finished = true
		default:
		}
		if !blocked && !finished {
			time.Sleep(10 * time.Millisecond)
		}
	}
	unlockErr := transaction.Rollback(ctx)
	if !finished {
		migrationErr = <-result
	}
	connection.Release()
	if observationErr != nil || unlockErr != nil || !blocked || migrationErr != nil {
		t.Fatalf("migration policy serialization: blocked=%t observation=%v unlock=%v migration=%v", blocked, observationErr, unlockErr, migrationErr)
	}
}
