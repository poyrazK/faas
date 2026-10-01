//go:build !no_pg

// adr: 375
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func policyTestNode(t *testing.T, store *state.PgStore) state.ComputeNode {
	t.Helper()
	role, target := "compute-only", "tcp://127.0.0.1:9090"
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "policy-fence-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &target})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func policyTestEpoch(t *testing.T, ctx context.Context, store *state.PgStore, node, boot string) state.GatewayTrafficEpoch {
	t.Helper()
	old, err := store.ReadGatewayTrafficEpoch(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := store.RegisterGatewayTrafficEpoch(ctx, node, boot, old.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReportGatewayTrafficRuntime(ctx, epoch, state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}); err != nil {
		t.Fatal(err)
	}
	return epoch
}

func reportPolicyTestProgress(t *testing.T, ctx context.Context, store *state.PgStore, kind state.GatewayPolicyKind, node, boot string, cursor int64) error {
	t.Helper()
	return store.ReportGatewayPolicyProgress(ctx, policyTestEpoch(t, ctx, store, node, boot), kind, cursor)
}

func policyTestRead(t *testing.T, store *state.PgStore, node string) state.ServingGatewayControlPlaneState {
	t.Helper()
	rows, err := store.ListServingGatewayControlPlaneStates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.NodeName == node {
			return row
		}
	}
	t.Fatalf("missing serving node %s", node)
	return state.ServingGatewayControlPlaneState{}
}

var policyTestKinds = []state.GatewayPolicyKind{state.GatewayPolicyControlPlane, state.GatewayPolicyEdgeRules, state.GatewayPolicyCorsPresets, state.GatewayPolicyCachePurge}

func assertPolicyTestCursors(t *testing.T, row state.ServingGatewayControlPlaneState, want int64) {
	t.Helper()
	if row.LastChangeID != want || row.LastEdgeRuleChangeID != want || row.LastCorsPresetChangeID != want || row.LastResponseCachePurgeID != want {
		t.Fatalf("progress = %+v, want all %d", row, want)
	}
}

func TestPgGatewayPolicyProgressFencesReplacementAndLegacyWriters(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	node := policyTestNode(t, store)
	a := policyTestEpoch(t, t.Context(), store, node.Name, uuid.NewString())
	for _, kind := range policyTestKinds {
		for _, cursor := range []int64{8, 7} {
			if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, cursor); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 8)
	b, err := store.RegisterGatewayTrafficEpoch(t.Context(), node.Name, uuid.NewString(), a.Generation)
	if err != nil {
		t.Fatal(err)
	}
	assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 0)
	for _, kind := range policyTestKinds {
		if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, 99); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			t.Fatalf("old %s acknowledgement = %v", kind, err)
		}
		if err := store.ReportGatewayPolicyProgress(t.Context(), b, kind, 2); err != nil {
			t.Fatal(err)
		}
	}
	// Progress cannot establish serving readiness by itself.
	assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 0)
	if err := store.ReportGatewayTrafficRuntime(t.Context(), b, state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		store.UpsertGatewayControlPlaneWatermark(t.Context(), node.Name, a.BootID, 99),
		store.UpsertGatewayEdgeRuleWatermark(t.Context(), node.Name, a.BootID, 99),
		store.UpsertGatewayCorsPresetWatermark(t.Context(), node.Name, a.BootID, 99),
		store.UpsertGatewayResponseCachePurgeWatermark(t.Context(), node.Name, 99),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 2)
	if err := store.RetireGatewayTrafficRuntime(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 0)
	if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(t.Context(), node.Name); err != nil || cursor != 2 {
		t.Fatalf("durable restart bootstrap = %d, %v", cursor, err)
	}
	if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(t.Context(), "new-peer"); err != nil || cursor != 0 {
		t.Fatalf("retired peer bootstrap = %d, %v", cursor, err)
	}
}

func TestPgGatewayPolicyProgressWaiterCannotAcknowledgeReplacement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	node := policyTestNode(t, store)
	a := policyTestEpoch(t, t.Context(), store, node.Name, uuid.NewString())
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	boot := uuid.New()
	generation, err := sqlc.New().RegisterGatewayTrafficRuntimeEpoch(t.Context(), tx, sqlc.RegisterGatewayTrafficRuntimeEpochParams{NodeName: node.Name, BootID: pgtype.UUID{Bytes: boot, Valid: true}, ExpectedGeneration: a.Generation})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- store.ReportGatewayPolicyProgress(ctx, a, state.GatewayPolicyControlPlane, 99) }()
	// Wait for PostgreSQL to confirm the old publisher is blocked on the
	// replacement's epoch lock; committing then exercises row rechecking.
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock' AND query LIKE '-- name: ReportGatewayPolicyProgress%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("old publisher did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
		t.Fatalf("blocked old acknowledgement = %v", err)
	}
	b := state.GatewayTrafficEpoch{NodeName: node.Name, Generation: generation, BootID: boot.String()}
	if err := store.ReportGatewayPolicyProgress(ctx, b, state.GatewayPolicyControlPlane, 1); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	if err := pool.QueryRow(ctx, `SELECT last_change_id FROM gateway_traffic_policy_observations WHERE node_name=$1 AND policy_kind='control_plane'`, node.Name).Scan(&cursor); err != nil || cursor != 1 {
		t.Fatalf("replacement cursor = %d, %v", cursor, err)
	}
}

func TestPgGatewayPolicyProgressFreshnessAndCacheBootstrap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	node := policyTestNode(t, store)
	a := policyTestEpoch(t, t.Context(), store, node.Name, uuid.NewString())
	for _, kind := range policyTestKinds {
		if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, 4); err != nil {
			t.Fatal(err)
		}
	}
	if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(t.Context(), "new-peer"); err != nil || cursor != 4 {
		t.Fatalf("fresh peer bootstrap = %d, %v", cursor, err)
	}
	for _, tc := range []struct {
		name    string
		seconds int
	}{{"stale", -60}, {"future", 60}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(t.Context(), `UPDATE gateway_traffic_runtime_observations SET reported_at=clock_timestamp()+make_interval(secs=>$2) WHERE node_name=$1`, node.Name, tc.seconds); err != nil {
				t.Fatal(err)
			}
			assertPolicyTestCursors(t, policyTestRead(t, store, node.Name), 0)
			if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(t.Context(), "new-peer"); err != nil || cursor != 0 {
				t.Fatalf("invalid peer bootstrap = %d, %v", cursor, err)
			}
		})
	}
	if err := store.ReportGatewayTrafficRuntime(t.Context(), a, state.GatewayTrafficFeatures{RateCounterMode: "local", RetryCounterMode: "local"}); err != nil {
		t.Fatal(err)
	}
	row := policyTestRead(t, store, node.Name)
	assertPolicyTestCursors(t, row, 4)
	if row.DatabaseNow.IsZero() || row.ObservedAt.After(row.DatabaseNow) {
		t.Fatalf("database clock = %+v", row)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE gateway_traffic_policy_observations SET observed_at=clock_timestamp()-interval '1 minute' WHERE node_name=$1 AND policy_kind='cache_purge'`, node.Name); err != nil {
		t.Fatal(err)
	}
	if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(t.Context(), "new-peer"); err != nil || cursor != 0 {
		t.Fatalf("stale policy peer bootstrap = %d, %v", cursor, err)
	}
}

func seedPolicyTestHistory(t *testing.T, pool *pgxpool.Pool, kind state.GatewayPolicyKind) (int64, func(context.Context, time.Time) (int64, error)) {
	t.Helper()
	store := state.NewPgStore(pool)
	id := uuid.NewString()
	if kind == state.GatewayPolicyCachePurge {
		account, err := store.CreateAccount(t.Context(), "purge-history-"+id+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "purge-history-" + id[:8], Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		id = app.ID
	}
	var last int64
	for range 2 {
		var err error
		switch kind {
		case state.GatewayPolicyControlPlane:
			err = pool.QueryRow(t.Context(), `INSERT INTO control_plane_change_log(resource_type,resource_id,app_id,operation,created_at) VALUES('app',$1,$1,'updated',now()-interval '40 days') RETURNING id`, id).Scan(&last)
		case state.GatewayPolicyEdgeRules:
			err = pool.QueryRow(t.Context(), `INSERT INTO edge_rule_change_log(app_id,rule_id,operation,created_at) VALUES($1,$2,'created',now()-interval '40 days') RETURNING id`, id, uuid.NewString()).Scan(&last)
		case state.GatewayPolicyCorsPresets:
			err = pool.QueryRow(t.Context(), `INSERT INTO cors_preset_change_log(account_id,preset_id,operation,created_at) VALUES($1,$2,'updated',now()-interval '40 days') RETURNING id`, id, uuid.NewString()).Scan(&last)
		case state.GatewayPolicyCachePurge:
			err = pool.QueryRow(t.Context(), `INSERT INTO response_cache_purge_change_log(app_id,path_glob,tag,created_at) VALUES($1,'*','',now()-interval '40 days') RETURNING id`, id).Scan(&last)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	switch kind {
	case state.GatewayPolicyControlPlane:
		return last, store.PruneControlPlaneChangeLog
	case state.GatewayPolicyEdgeRules:
		return last, store.PruneEdgeRuleChangeLog
	case state.GatewayPolicyCorsPresets:
		return last, store.PruneCorsPresetChangeLog
	default:
		return last, store.PruneResponseCachePurgeChangeLog
	}
}

func TestPgGatewayPolicyPrunersRequireFreshCurrentAcknowledgements(t *testing.T) {
	for _, kind := range policyTestKinds {
		t.Run(string(kind), func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			node := policyTestNode(t, store)
			last, prune := seedPolicyTestHistory(t, pool, kind)
			cutoff := time.Now().Add(-30 * 24 * time.Hour)
			for _, err := range []error{store.UpsertGatewayControlPlaneWatermark(t.Context(), node.Name, uuid.NewString(), last), store.UpsertGatewayEdgeRuleWatermark(t.Context(), node.Name, uuid.NewString(), last), store.UpsertGatewayCorsPresetWatermark(t.Context(), node.Name, uuid.NewString(), last), store.UpsertGatewayResponseCachePurgeWatermark(t.Context(), node.Name, last)} {
				if err != nil {
					t.Fatal(err)
				}
			}
			checkHeld := func() {
				t.Helper()
				if n, err := prune(t.Context(), cutoff); err != nil || n != 0 {
					t.Fatalf("unacknowledged pruning = %d, %v", n, err)
				}
			}
			checkHeld()
			a := policyTestEpoch(t, t.Context(), store, node.Name, uuid.NewString())
			if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, last); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE gateway_traffic_policy_observations SET observed_at=clock_timestamp()-interval '1 minute' WHERE node_name=$1`, node.Name); err != nil {
				t.Fatal(err)
			}
			checkHeld()
			if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, last); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE gateway_traffic_runtime_observations SET reported_at=clock_timestamp()-interval '1 minute' WHERE node_name=$1`, node.Name); err != nil {
				t.Fatal(err)
			}
			checkHeld()
			b := policyTestEpoch(t, t.Context(), store, node.Name, uuid.NewString())
			checkHeld()
			if err := store.ReportGatewayPolicyProgress(t.Context(), a, kind, last); !errors.Is(err, state.ErrGatewayTrafficEpochLost) {
				t.Fatal(err)
			}
			checkHeld()
			if err := store.ReportGatewayPolicyProgress(t.Context(), b, kind, last); err != nil {
				t.Fatal(err)
			}
			want := int64(2)
			if kind == state.GatewayPolicyCachePurge {
				want = 1
			}
			if n, err := prune(t.Context(), cutoff); err != nil || n != want {
				t.Fatalf("fresh current pruning = %d, %v, want %d", n, err, want)
			}
		})
	}
}
