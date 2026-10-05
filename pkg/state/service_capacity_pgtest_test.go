// adr: 422
package state_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

// ADR-422: the database is the admission boundary, including direct SQL and
// restarted clients. Replaying the migration must preserve operator policy.
func TestServiceCapacityPostgresRawSQLRestartAndReplay(t *testing.T) {
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
	var fleet []state.ComputeNode
	for range 2 {
		n, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "capacity-" + uuid.NewString(), TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	stopHeartbeats := conformance.KeepNodesHeartbeating(t, store, fleet)
	a, err := store.CreateApp(ctx, state.App{AccountID: fx.Account.ID, Slug: "protected-service", RAMMB: 512, MaxConcurrency: 20, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Desired: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET manifest=jsonb_set(manifest,'{service_replicas,desired}','9') WHERE id=$1`, a.ID); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("raw SQL bypassed protection: %v", err)
	}
	restarted := state.NewPgStore(pool)
	r, err := restarted.ServiceCapacityProtection(ctx)
	if err != nil || !r.Enabled || r.State != "protected" || r.ReservedReplicas != 8 {
		t.Fatalf("restart lost policy or intent: %+v %v", r, err)
	}
	// Replay every ADR-422 migration in order: the definition's CREATE OR
	// REPLACE drops the later function-level jit=off, which the follow-up
	// migration restates.
	for _, name := range []string{
		"20261001084654053_service_capacity_protection.sql",
		"20261004191632612_service_capacity_snapshot_without_jit.sql",
	} {
		data, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(strings.Split(string(data), "-- +goose Up\n")[1], "-- +goose Down\n")[0]
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("migration replay %s: %v", name, err)
		}
	}
	r, err = restarted.ServiceCapacityProtection(ctx)
	if err != nil || !r.Enabled || r.State != "protected" || r.ReservedReplicas != 8 {
		t.Fatalf("replay changed protection: %+v %v", r, err)
	}
	var snapshotConfig string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_to_string(proconfig, ','), '') FROM pg_proc WHERE oid = to_regprocedure('service_capacity_snapshot()')`).Scan(&snapshotConfig); err != nil || !strings.Contains(snapshotConfig, "jit=off") {
		t.Fatalf("replay left snapshot JIT enabled: config=%q err=%v", snapshotConfig, err)
	}
	var overhead, overcommit, startup, heartbeat int
	var vcpuMap []byte
	if err := pool.QueryRow(ctx, `SELECT overhead_mb,cpu_overcommit,startup_cpu,heartbeat_seconds,plan_vcpus FROM service_capacity_policy`).Scan(&overhead, &overcommit, &startup, &heartbeat, &vcpuMap); err != nil {
		t.Fatal(err)
	}
	if overhead != api.PerVMOverheadMB || overcommit != api.CPUOvercommit || startup != api.DefaultAppCPUMillicores || heartbeat != int(state.DefaultHeartbeatStaleness.Seconds()) {
		t.Fatalf("SQL policy drift: overhead=%d CPU factor=%d startup=%d heartbeat=%d", overhead, overcommit, startup, heartbeat)
	}
	var planVCPUs map[api.Plan]int
	if err := json.Unmarshal(vcpuMap, &planVCPUs); err != nil {
		t.Fatal(err)
	}
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if got, want := planVCPUs[plan], api.MustLimitsFor(plan).VCPU; got != want {
			t.Fatalf("SQL policy vCPU drift for %s: got %d, want %d", plan, got, want)
		}
	}
	// Request counters must not take the fleet lock or scan declarations.
	var definition string
	if err := pool.QueryRow(ctx, `SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid='instances'::regclass AND tgname='service_capacity_before'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "UPDATE OF") || strings.Contains(definition, "request_count") {
		t.Fatalf("telemetry takes the admission lock: %s", definition)
	}
	// Freshness remains a production admission bound. Age one otherwise
	// active host without waiting for wall time and recover only on its peer.
	stopHeartbeats()
	if _, err := pool.Exec(ctx, `UPDATE compute_nodes SET last_heartbeat_at=now()-make_interval(secs=>$2) WHERE id=$1`, fleet[0].ID, int(2*state.DefaultHeartbeatStaleness.Seconds())); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatComputeNode(ctx, fleet[1].ID); err != nil {
		t.Fatal(err)
	}
	r, err = restarted.ServiceCapacityProtection(ctx)
	if err != nil || r.HealthyNodes != 1 || r.State != "degraded" {
		t.Fatalf("stale active host counted as healthy: %+v %v", r, err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("stale active host accepted recovery: %v", err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[1].ID, uuid.NewString(), string(state.InstanceModeService)); err != nil {
		t.Fatalf("fresh peer refused recovery: %v", err)
	}
}
