//go:build !no_pg

// adr: 570
package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func trafficSessionTestNode(t *testing.T, store *state.PgStore) state.ComputeNode {
	t.Helper()
	role, target := "compute-only", "tcp://127.0.0.1:9090"
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "policy-session-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &target})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestTrafficRuntimeSessionCapturesBaselineBeforeConsumersAndFencesDelayedStart(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	node := trafficSessionTestNode(t, store)
	delayed, err := newTrafficRuntimeSession(t.Context(), store, node.Name)
	if err != nil {
		t.Fatal(err)
	}
	repair := &gatewayPolicyRepairStore{PgStore: store, session: delayed}
	if err := repair.UpsertGatewayControlPlaneWatermark(t.Context(), node.Name, uuid.NewString(), 99); err != nil {
		t.Fatal(err)
	}
	epoch, err := store.ReadGatewayTrafficEpoch(t.Context(), node.Name)
	if err != nil || epoch.Generation != 0 {
		t.Fatalf("pre-listener repair allocated epoch = %+v, %v", epoch, err)
	}
	replacement, err := newTrafficRuntimeSession(t.Context(), store, node.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.register(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := delayed.register(t.Context()); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("delayed registration rebased: %v", err)
		}
		if err := repair.UpsertGatewayControlPlaneWatermark(t.Context(), node.Name, uuid.NewString(), 99); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("delayed repair publication = %v", err)
		}
	}
}

func TestTrafficRuntimeSessionSharesGenerationAcrossActualRepairLoops(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	node := trafficSessionTestNode(t, store)
	session, err := newTrafficRuntimeSession(t.Context(), store, node.Name)
	if err != nil {
		t.Fatal(err)
	}
	repair := &gatewayPolicyRepairStore{PgStore: store, session: session}
	ctx, cancel := context.WithCancel(t.Context())
	var consumers sync.WaitGroup
	start := func(run func()) { consumers.Add(1); go func() { defer consumers.Done(); run() }() }
	start(func() {
		watchDurableControlPlaneChanges(ctx, repair, new(controlPlaneRepairInvalidatorFake), discardLogger(), node.Name)
	})
	start(func() {
		watchDurableEdgeRuleChanges(ctx, repair, new(edgeRuleRepairInvalidatorFake), discardLogger(), node.Name)
	})
	start(func() {
		watchDurableCorsPresetChanges(ctx, repair, new(corsPresetRepairInvalidatorFake), discardLogger(), node.Name)
	})
	start(func() {
		watchDurableResponseCachePurges(ctx, repair, new(responseCachePurgeRepairInvalidatorFake), discardLogger(), node.Name)
	})
	t.Cleanup(func() { cancel(); consumers.Wait() })
	stop := startTrafficRuntimeObserver(ctx, session, state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}, func() bool { return true }, discardLogger())
	t.Cleanup(stop)
	deadline, stopWait := context.WithTimeout(t.Context(), 8*time.Second)
	defer stopWait()
	var count int
	for {
		if err := pool.QueryRow(deadline, `SELECT count(*) FROM gateway_traffic_policy_observations p JOIN gateway_traffic_runtime_observations o USING(node_name,generation,boot_id) WHERE p.node_name=$1 AND o.reported_at IS NOT NULL`, node.Name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 4 {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatalf("repair components = %d, want 4: %v", count, deadline.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	a, lost := session.current()
	if lost || a.Generation == 0 {
		t.Fatalf("serving session = %+v lost=%v", a, lost)
	}
	// Commit mutations after the initial acknowledgements. Each actual loop
	// must replay its own ledger before advancing the common process session.
	account, err := store.CreateAccount(t.Context(), "repair-session-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "repair-session-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	var edgeID, corsID int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO edge_rule_change_log(app_id,rule_id,operation) VALUES($1,$2,'created') RETURNING id`, app.ID, uuid.NewString()).Scan(&edgeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO cors_preset_change_log(account_id,preset_id,operation) VALUES($1,$2,'updated') RETURNING id`, account.ID, uuid.NewString()).Scan(&corsID); err != nil {
		t.Fatal(err)
	}
	controlID, err := store.LatestControlPlaneChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	purgeID, err := store.CreateResponseCachePurge(t.Context(), app.ID, "*", "")
	if err != nil {
		t.Fatal(err)
	}
	for {
		rows, err := store.ListServingGatewayControlPlaneStates(deadline)
		if err != nil {
			t.Fatal(err)
		}
		applied := false
		for _, row := range rows {
			if row.NodeName == node.Name && row.LastChangeID >= controlID && row.LastEdgeRuleChangeID == edgeID && row.LastCorsPresetChangeID == corsID && row.LastResponseCachePurgeID == purgeID {
				applied = true
			}
		}
		if applied {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatalf("repair did not replay committed mutations: %v", deadline.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	b, err := store.RegisterGatewayTrafficEpoch(t.Context(), node.Name, uuid.NewString(), a.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := repair.UpsertGatewayControlPlaneWatermark(t.Context(), node.Name, uuid.NewString(), 99); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		t.Fatalf("replaced repair = %v", err)
	}
	for _, err := range []error{repair.UpsertGatewayEdgeRuleWatermark(t.Context(), node.Name, uuid.NewString(), 99), repair.UpsertGatewayCorsPresetWatermark(t.Context(), node.Name, uuid.NewString(), 99), repair.UpsertGatewayResponseCachePurgeWatermark(t.Context(), node.Name, 99)} {
		if !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("shared loss = %v", err)
		}
	}
	stop()
	cancel()
	consumers.Wait()
	current, err := store.ReadGatewayTrafficEpoch(t.Context(), node.Name)
	if err != nil || current != b {
		t.Fatalf("old shutdown replaced current epoch = %+v, %v", current, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM gateway_traffic_policy_observations WHERE node_name=$1 AND generation=$2`, node.Name, b.Generation).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old repair acknowledged replacement: %d, %v", count, err)
	}
}
