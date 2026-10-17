//go:build !no_pg

// adr: 570
package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgGatewayTrafficRuntimeReplacementFencesOldWriters(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	node := createTrafficObservationNode(t, store)
	bootA, bootB := uuid.NewString(), uuid.NewString()
	a, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, bootA, 0)
	if err != nil {
		t.Fatal(err)
	}
	features := state.GatewayTrafficFeatures{RetryEnabled: true, RateCounterMode: "central", RetryCounterMode: "shared", RetryBackendID: "0123456789abcdef", DeadlineSigning: true, PolicySnapshot: true, SecurityRevocation: true, ManagedHTTP: true, ManagedCircuit: true}
	if err := store.ReportGatewayTrafficRuntime(ctx, a, features); err != nil {
		t.Fatal(err)
	}
	row := findTrafficObservation(t, store, node.Name)
	if row.GatewayTrafficFeatures != features || row.ReportedAt.IsZero() || row.DatabaseNow.Before(row.ReportedAt) {
		t.Fatalf("reported features = %+v", row)
	}
	b, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, bootB, a.Generation)
	if err != nil || b.Generation <= a.Generation {
		t.Fatalf("replacement = %+v, %v", b, err)
	}
	row = findTrafficObservation(t, store, node.Name)
	if !row.ReportedAt.IsZero() || row.RateCounterMode != "unwired" || row.RetryEnabled {
		t.Fatalf("replacement retained old report: %+v", row)
	}
	// Recover a registration whose successful response was lost. Its original
	// baseline remains fixed; the sequence must not advance a second time.
	recovered, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, bootB, a.Generation)
	if err != nil || recovered != b {
		t.Fatalf("lost response recovery = %+v, %v", recovered, err)
	}
	for _, write := range []func() error{
		func() error { return store.ReportGatewayTrafficRuntime(ctx, a, features) },
		func() error { return store.RetireGatewayTrafficRuntime(ctx, a) },
		func() error { _, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, bootA, 0); return err },
	} {
		if err := write(); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("old writer = %v", err)
		}
	}
	features = state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}
	if err := store.ReportGatewayTrafficRuntime(ctx, b, features); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireGatewayTrafficRuntime(ctx, a); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		t.Fatal(err)
	}
	row = findTrafficObservation(t, store, node.Name)
	if row.GatewayTrafficFeatures != features || row.ReportedAt.IsZero() {
		t.Fatalf("old retirement cleared replacement: %+v", row)
	}
	if err := store.RetireGatewayTrafficRuntime(ctx, b); err != nil {
		t.Fatal(err)
	}
	if row := findTrafficObservation(t, store, node.Name); !row.ReportedAt.IsZero() {
		t.Fatalf("retirement retained freshness: %+v", row)
	}
}

func TestPgGatewayTrafficRuntimeConcurrentRegistration(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	node := createTrafficObservationNode(t, store)
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, uuid.NewString(), 0)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent registration winners = %d", winners)
	}
}

func TestPgGatewayTrafficRuntimeRosterAndValidation(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	node := createTrafficObservationNode(t, store)
	row := findTrafficObservation(t, store, node.Name)
	if row.Generation != 0 || !row.ReportedAt.IsZero() || row.RateCounterMode != "unwired" {
		t.Fatalf("missing daemon report = %+v", row)
	}
	epoch, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, uuid.NewString(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []state.GatewayTrafficFeatures{
		{RateCounterMode: "invalid", RetryCounterMode: "local"},
		{RateCounterMode: "central", RetryCounterMode: "shared"},
		{RateCounterMode: "central", RetryCounterMode: "redis", RetryBackendID: "redis://secret@host"},
		{RateCounterMode: "local", RetryCounterMode: "local", RetryBackendID: "0123456789abcdef"},
		{RateCounterMode: "central", RetryCounterMode: "local", ManagedCircuit: true},
	} {
		if err := store.ReportGatewayTrafficRuntime(ctx, epoch, f); err == nil {
			t.Fatalf("invalid features accepted: %+v", f)
		}
	}
	for _, update := range []string{
		`UPDATE compute_nodes SET lifecycle = 'draining' WHERE name = $1`,
		`UPDATE compute_nodes SET lifecycle = 'active', gateway_target_url = NULL WHERE name = $1`,
		`UPDATE compute_nodes SET gateway_target_url = 'tcp://127.0.0.1:9090', role = 'control-plane' WHERE name = $1`,
	} {
		if _, err := pool.Exec(ctx, update, node.Name); err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListServingGatewayTrafficRuntime(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.NodeName == node.Name {
				t.Fatalf("nonserving node in roster: %+v", r)
			}
		}
		if _, err := store.RegisterGatewayTrafficEpoch(ctx, node.Name, uuid.NewString(), epoch.Generation); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("nonserving registration = %v", err)
		}
	}
	if err := store.DeleteComputeNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	current, err := store.ReadGatewayTrafficEpoch(ctx, node.Name)
	if err != nil || current.Generation != 0 {
		t.Fatalf("deleted node observation = %+v, %v", current, err)
	}
	if err := store.ReportGatewayTrafficRuntime(ctx, epoch, state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		t.Fatalf("deleted writer = %v", err)
	}
}

func TestPgGatewayTrafficRuntimeRefusesTruncatedRoster(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	// Bulk fixture setup stays in the test; the production reader uses sqlc.
	_, err := pool.Exec(t.Context(), `INSERT INTO compute_nodes
		(name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, role, gateway_target_url)
		SELECT 'bounded-traffic-' || i, 'unix:///run/bounded-' || i || '.sock', 1, 1024, 1, 512,
		'compute-only', 'tcp://127.0.0.1:9090' FROM generate_series(1, $1::integer) i`, api.TrafficRuntimeObservationMaxNodes+1)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.NewPgStore(pool).ListServingGatewayTrafficRuntime(t.Context())
	if err == nil || rows != nil {
		t.Fatalf("oversized roster = %d rows, %v", len(rows), err)
	}
}

func createTrafficObservationNode(t *testing.T, store *state.PgStore) state.ComputeNode {
	t.Helper()
	role, target := "compute-only", "tcp://127.0.0.1:9090"
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "traffic-observation-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1,
		Active: true, Role: &role, GatewayTargetURL: &target,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteComputeNode(context.Background(), node.ID) })
	return node
}

func findTrafficObservation(t *testing.T, store *state.PgStore, node string) state.ServingGatewayTrafficRuntime {
	t.Helper()
	rows, err := store.ListServingGatewayTrafficRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.NodeName == node {
			return row
		}
	}
	t.Fatalf("node %s absent from serving roster", node)
	return state.ServingGatewayTrafficRuntime{}
}
