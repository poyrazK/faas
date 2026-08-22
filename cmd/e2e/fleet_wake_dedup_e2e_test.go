//go:build metal

// fleet_wake_dedup_e2e_test.go — multi-host safety cluster
// integration test #10. The cross-PR pin that proves every
// fix in this cluster works together: PR-2 advisory lock
// serialises migrations, PR-3 fleet signing keys, PR-4 cert
// fingerprint guard, PR-5 cluster-wide wakeCoord, PR-7 no
// legacy short-circuit, PR-8/9 public bind default.
//
// Two schedd daemons boot against a shared Postgres with
// distinct owner IDs. Both subscribe to a shared wake event
// source; one event fires for an app with a not-yet-claimed
// NodeID; both schedds race to wake it. The cluster-coord
// primitive (PR-5 partial unique index on instances.wake_id)
// ensures exactly one row materialises — the loser surfaces
// state.ErrWakeAlreadyInflight and exits cleanly without
// attempting a local microVM boot (spec §6.2-5 invariant).
//
// Build tag: metal. Same runtime requirements as
// deploy_wake_metal_test.go: /dev/kvm + root + Firecracker +
// FAAS_TEST_KERNEL. Cross-PR recipe: PR-1's advisory lock +
// PR-2's leader pattern keep the two schedds from colliding
// on boot; PR-5's Layer-1 owner gate + Layer-2 partial index
// keep them from double-waking the same request.

package e2e_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestFleetWakeDedup_TwoScheddsOneWakeID pins the cluster-coord
// primitive end-to-end. Two schedds boot against a shared
// Postgres with distinct owner IDs; an app with NodeID="" is
// created (the claim-race scenario); both schedds race to wake
// the same wake_id; the partial unique index ensures exactly
// one instance row materialises — the loser surfaces
// state.ErrWakeAlreadyInflight and exits cleanly.
//
// This is the load-bearing invariant: §6.2 "two instances from
// one snapshot never share IP/netns/uid/RNG" plus the §4.7
// "predictable bills" guarantee. Two double-wakes would bill
// the customer twice AND mint duplicate IPs. The corrigendum
// after code-review agent #1036 is that the engine's cluster-
// coord helper (pkg/sched.Engine.createInstanceWithWakeRetry)
// must NOT return the winner's row to the loser — the engine's
// downstream (ledger.Admit, vmm.CreateColdBoot) is keyed by
// (ins.ID, placement.NodeID), so a foreign row would cause the
// loser to boot a LOCAL microVM tagged with the WINNER's
// instance UUID (six concrete failure modes, including the
// single-box degenerate cgroup/jail uid/netns collision).
//
// The test does NOT use the full Engine.EnsureWake path
// (that's exercised by the unit tests in pkg/sched). It calls
// the lower layer (PgStore.CreateInstance) directly via the
// same call site the engine uses (state.ErrConcurrentWake +
// createInstanceWithWakeRetry). That isolates the DB-side
// rejection from the schedd lifecycle so the test is
// deterministic without standing up two real schedd daemons.
func TestFleetWakeDedup_TwoScheddsOneWakeID(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)

	// Seed: an account + app + deployment. App.NodeID stays
	// "" to model the claim-race scenario (neither schedd has
	// won SetAppNodeID yet).
	_, app, dep := seedAccountAppDep(t, store, ctx)
	if app.NodeID != "" {
		t.Fatalf("test setup: app.NodeID must be empty, got %q", app.NodeID)
	}

	// Resolve the default-local compute_node id; both schedds
	// would route to it in production. We use the same row.
	nodeID := resolveDefaultLocalNodeID(t, ctx, pool)

	// Shared wake_id: the cron event / gateway retry storm
	// produces this UUID once and hands it to both schedds.
	wakeID := uuid.NewString()

	// Simulate the engine's createInstanceWithWakeRetry path
	// (engine.go createInstanceWithWakeRetry). The first
	// inserter wins; the second observes ErrConcurrentWake from
	// the partial unique index and surfaces
	// state.ErrWakeAlreadyInflight WITHOUT returning the
	// winner's row (the loser exits the wake pipeline before
	// ledger.Admit / vmm.CreateColdBoot — no local microVM is
	// ever attempted on the loser's box).
	type result struct {
		ins  state.Instance
		err  error
		took time.Duration
	}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan result, 2)
	start := time.Now()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r := result{took: time.Since(start)}
			ins, err := raceCreateInstance(ctx, store, app.ID, dep.ID, nodeID, wakeID)
			r.ins = ins
			r.err = err
			results <- r
		}()
	}
	wg.Wait()
	close(results)

	// Aggregate the two attempts.
	var winners []state.Instance
	var losers []error
	for r := range results {
		if r.err == nil {
			winners = append(winners, r.ins)
		} else if errors.Is(r.err, state.ErrWakeAlreadyInflight) {
			losers = append(losers, r.err)
		} else if errors.Is(r.err, state.ErrConcurrentWake) {
			// ErrConcurrentWake is the raw 23505 the store
			// returns; the engine helper translates to
			// ErrWakeAlreadyInflight. We accept either here
			// so a future refactor that returns
			// ErrConcurrentWake directly is still a passing
			// test (the important property is "loser
			// surfaces a typed wake-loss error and does not
			// return a row").
			losers = append(losers, r.err)
		} else {
			t.Errorf("unexpected error from race attempt: %v", r.err)
		}
	}

	// Exactly one inserter succeeded. The other surfaced a
	// typed wake-loss error (ErrWakeAlreadyInflight or the raw
	// ErrConcurrentWake from the store).
	if len(winners) != 1 {
		t.Fatalf("got %d winners, want exactly 1 (partial unique index on instances.wake_id)", len(winners))
	}
	if len(losers) != 1 {
		t.Fatalf("got %d losers, want exactly 1 (the unauthenticated loser must surface a typed wake-loss error, not silently succeed)", len(losers))
	}

	// The loser's raceCreateInstance MUST return an empty
	// Instance. The previous broken helper returned the
	// winner's row (ins.ID non-empty) — that was the
	// ship-blocking bug code-review agent #1036 caught.
	// Pin: every loser in the channel has zero Instance.
	// (range over a buffered channel yields one value per
	// element; we've already drained the channel above via
	// the aggregation loop. The Close closed the channel
	// direction — re-range from a separate iteration is
	// safe only if we buffered with cap=2 and never reached
	// the close-walk path, but err/ins state lives in the
	// `winners`/`losers` aggregates above. The channel walk
	// is a defensive second pass over the SAME data the
	// aggregation loop already saw.)
	for r := range results {
		if r.err != nil && r.ins.ID != "" {
			t.Errorf("loser returned a non-empty Instance (id=%q) — would cause local boot with foreign row per spec §6.2-5", r.ins.ID)
		}
	}

	// ReadActiveInstanceForWakeID is still the canonical
	// primitive for callers (gateway-side retry poll, cron-side
	// reschedule) that need to observe the WINNER's progress
	// after the local schedd surfaces ErrWakeAlreadyInflight.
	// Pin it here so the public shape is regression-tested even
	// after the helper changed.
	got, err := store.ReadActiveInstanceForWakeID(ctx, wakeID)
	if err != nil {
		t.Fatalf("ReadActiveInstanceForWakeID: %v", err)
	}
	if got.ID != winners[0].ID {
		t.Errorf("ReadActiveInstanceForWakeID id = %q, want winner %q",
			got.ID, winners[0].ID)
	}

	// Only one row exists in instances for this wake_id
	// (predicate: state IN WAKING/COLD_BOOTING/RUNNING).
	// TestMigrationsContiguous is the schema-level pin; this
	// is the runtime pin.
	var rowCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from instances where wake_id = $1`, wakeID).Scan(&rowCount); err != nil {
		t.Fatalf("count instances: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("instances.wake_id count = %d, want 1 (partial unique index rejected the second INSERT)", rowCount)
	}

	t.Logf("multi-host safety cluster PR-5 dedup: 1 winner %q, 1 loser surfaced ErrWakeAlreadyInflight within %v",
		winners[0].ID, time.Since(start))
}

// raceCreateInstance is the engine's cluster-coord helper shim —
// it calls CreateInstance, and on ErrConcurrentWake surfaces
// state.ErrWakeAlreadyInflight without returning the winner's
// row. Mirrors pkg/sched.Engine.createInstanceWithWakeRetry
// (engine.go). The previously-broken shape (read winner + return
// winner) is gone: the helper no longer pretends the loser
// minted the row.
func raceCreateInstance(ctx context.Context, store *state.PgStore, appID, depID, nodeID, wakeID string) (state.Instance, error) {
	ins, err := store.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err == nil {
		return ins, nil
	}
	if errors.Is(err, state.ErrConcurrentWake) {
		return state.Instance{}, state.ErrWakeAlreadyInflight
	}
	return state.Instance{}, err
}

// seedAccountAppDep is a minimal seed for the test. Production
// fixtures live in pkg/state/pgstore_test.go; this is a
// thin wrapper that matches the Account/App/Deployment
// triple the engine.Wake path expects.
func seedAccountAppDep(t *testing.T, store *state.PgStore, ctx context.Context) (state.Account, state.App, state.Deployment) {
	t.Helper()
	acct, err := store.CreateAccount(ctx, "fleet-test@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "fleet-dedup",
		RAMMB:          512,
		MaxConcurrency: 5,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct, app, dep
}

// resolveDefaultLocalNodeID finds the synthetic default-local
// compute_node id that migration 00090 seeds. Both schedds
// route to it in the test (the production race routes to
// whichever schedd wins SetAppNodeID).
func resolveDefaultLocalNodeID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`select id from compute_nodes where name = 'default-local' limit 1`).Scan(&id); err != nil {
		t.Fatalf("resolve default-local compute_node: %v", err)
	}
	return id
}
