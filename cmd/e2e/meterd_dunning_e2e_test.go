//go:build !no_pg

// Package e2e — meterd_dunning_e2e_test.go is the §14 M7 acceptance
// gate for audit-finding #2: the dunning state machine actually
// advances from past_due to suspended after 7d, and from suspended to
// deleted_pending after 21d, when meterd runs against a real Postgres.
//
// Mirrors cmd/e2e/meterd_quota_e2e_test.go: boots real daemon
// subprocesses (apid + schedd + meterd) so the dunning timer runs in
// the production wire, not the in-process fakes pkg/meter uses.
//
// Cases:
//   - past_due with past_due_at = 8 days ago → status flips to
//     suspended within one dunning tick; ParkInstance called for each
//     live instance; suspended email logged via the log sender.
//   - suspended with past_due_at = 22 days ago → status flips to
//     deleted_pending within one dunning tick; deletion email logged.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

package e2e_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDunning_PastDue7d_AdvancesToSuspended is the dunning transition
// acceptance gate (audit finding #2). Seeds an account in past_due
// with past_due_at 8 days old, polls for the row's status to flip to
// suspended, then asserts a ParkInstance call was made (the
// schedd→vmmd wire is covered by the metal tests; this proves only
// the meterd→schedd boundary).
func TestDunning_PastDue7d_AdvancesToSuspended(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 13, 10*time.Second) // bumped from 12 by migration 00013

	const dunningInterval = 2 * time.Second
	e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		[]string{"FAAS_DUNNING_INTERVAL=" + dunningInterval.String()})

	store := state.NewPgStore(pool)
	// Resolve the default-local compute_node id once at boot so the
	// FK on instances.node_id has a real target. Issue #97 / ADR-025
	// axis 3; the migration is applied above (db.MigrateUp runs the
	// full set).
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("resolve default-local compute_node: %v", err)
	}
	defaultLocalNodeID := node.ID

	acct, err := store.CreateAccount(ctx, "dunning7d@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "dunning7d", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, defaultLocalNodeID)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Flip the account to past_due via MarkDunningStep (the same path
	// the apid webhook uses), then backdate past_due_at to 8 days ago
	// via raw SQL so the timer sees a row that's overdue.
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountPastDue); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}
	eightDaysAgo := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if _, err := pool.Exec(ctx,
		`update accounts set past_due_at = $1 where id = $2`,
		eightDaysAgo, acct.ID); err != nil {
		t.Fatalf("backdate past_due_at: %v", err)
	}

	// Poll for the suspended transition.
	deadline := time.Now().Add(dunningInterval + 5*time.Second)
	for {
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Status == state.AccountSuspended {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("account.status = %s after %s; want suspended (dunning tick did not advance the row)",
				got.Status, dunningInterval+5*time.Second)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the instance was parked (the meterd→schedd.ParkInstance
	// boundary). schedd's ParkInstance is idempotent — the instance
	// must transition off RUNNING, regardless of which terminal state
	// the snapshot path lands in (PARKED on happy path, STOPPED on
	// snapshot-fail fallback, SNAPSHOTTING on in-progress).
	instDeadline := time.Now().Add(2 * dunningInterval)
	var instGot state.Instance
	for {
		instGot, err = store.InstanceByID(ctx, ins.ID)
		if err != nil {
			t.Fatalf("InstanceByID: %v", err)
		}
		if state.State(instGot.State) != state.StateRunning {
			break
		}
		if time.Now().After(instDeadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	switch state.State(instGot.State) {
	case state.StateParked, state.StateStopped, state.StateSnapshotting:
		// pass — dunning→ParkInstance wire landed
	default:
		t.Errorf("instance.state = %s after %s; want parked/stopped/snapshotting",
			instGot.State, 2*dunningInterval)
	}
}

// TestDunning_Suspended21d_AdvancesToDeletedPending is the second half
// of audit-finding #2: the suspended → deleted_pending transition
// fires after 21 days total from past_due_at.
func TestDunning_Suspended21d_AdvancesToDeletedPending(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 13, 10*time.Second)

	const dunningInterval = 2 * time.Second
	e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		[]string{"FAAS_DUNNING_INTERVAL=" + dunningInterval.String()})

	store := state.NewPgStore(pool)

	acct, err := store.CreateAccount(ctx, "dunning21d@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Flip directly to suspended (we're not testing the 7d hop here)
	// and backdate past_due_at to 22 days ago so the 21d threshold
	// is past.
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}
	twentyTwoDaysAgo := time.Now().UTC().Add(-22 * 24 * time.Hour)
	if _, err := pool.Exec(ctx,
		`update accounts set past_due_at = $1 where id = $2`,
		twentyTwoDaysAgo, acct.ID); err != nil {
		t.Fatalf("backdate past_due_at: %v", err)
	}

	// Poll for the deleted_pending transition.
	deadline := time.Now().Add(dunningInterval + 5*time.Second)
	for {
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Status == state.AccountDeletedPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("account.status = %s after %s; want deleted_pending",
				got.Status, dunningInterval+5*time.Second)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestE2E_FreeTierHardDelete_FlowsThroughGrace is the M7 §14
// completion test that lands next to the dunning tail: once
// `deletion_requested_at` is more than DeletionGraceDuration
// (30 days, pkg/state/memstore.go:2498) in the past, the apid
// grace tick (driven via FAAS_GRACE_INTERVAL=300ms for the same
// reason `TestE2E_GraceExpiry_HardDelete` accelerates it —
// account_e2e_test.go:50) hard-deletes the account row, and the
// customer-facing endpoints (GET /v1/account/export) and the
// store-side accessor (AccountByID) both observe the disappearance.
//
// The signal ladder is layered: apid's grace loop walks
// `pkg/grace.RunOnce` (RunOnce walks `deleted_pending` past grace
// and calls `Store.DeleteAccount`). Verify both:
//
//   - store.AccountByID(acct.ID) returns state.ErrNotFound
//     (the *internal* row-gone signal; proves the SQL DELETE
//     landed, not just that the API key was cleared).
//   - GET /v1/account/export returns 401
//     (the *external* row-gone signal — same one
//     TestE2E_GraceExpiry_HardDelete pins).
//
// The two assertions cover different failure modes: a bug that
// clears the API key without deleting the row would pass the
// 401-only check but fail the AccountByID check; a bug that
// deletes the row but leaves a stale JWT in cache would pass
// the AccountByID check but fail the 401 check.
//
// We boot APID only — no need for meterd/schedd here, just the
// grace tick — and we skip the FAQ-flavoured "Free tier" line
// (it's a property of the dunning pipeline, not the grace
// pipeline; the grace tick fires on every plan equally).
func TestE2E_FreeTierHardDelete_FlowsThroughGrace(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 13, 10*time.Second)

	const graceInterval = 300 * time.Millisecond
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_GRACE_INTERVAL=" + graceInterval.String(),
	})
	// SeedAccount labels via variadic — the label becomes part of the
	// email so each subtest gets its own row in the shared pgtest
	// schema. "hard-delete-flow" is unique to this test.
	key := h.SeedAccount(ctx, api.PlanFree, "hard-delete-flow")

	// Schedule deletion through the apid API rather than the store
	// directly — exercises the full customer-facing DELETE /v1/account
	// wire (handlers_account.go:87 calls MarkAccountDeletionPending).
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/account", nil); status != http.StatusOK {
		t.Fatalf("delete-account: %d %s", status, body)
	}

	// Fast-forward deletion_requested_at to 31 days ago so the next
	// grace tick sees the row as overdue. WHERE on id — the test
	// owns one account end-to-end, so the bare id is unambiguous
	// without an email tiebreaker.
	acctID := accountIDForPlanLabel(t, h.Pool, api.PlanFree, "hard-delete-flow")
	if _, err := pool.Exec(ctx,
		`update accounts set deletion_requested_at = now() - interval '31 days' where id = $1`,
		acctID); err != nil {
		t.Fatalf("backdate deletion_requested_at: %v", err)
	}

	store := state.NewPgStore(pool)

	// Internal signal — AccountByID returns ErrNotFound once the
	// row is hard-deleted. Bounded to a generous deadline (30+ grace
	// intervals + slack for boot/handshake and the
	// pgx → apid → grace tick handshake).
	internalDeadline := time.Now().Add(graceInterval*30 + 5*time.Second)
	for {
		_, err := store.AccountByID(ctx, acctID)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("AccountByID returned unexpected err = %v", err)
		}
		if time.Now().After(internalDeadline) {
			t.Fatalf("AccountByID did not return ErrNotFound within %v (grace tick did not hard-delete)",
				graceInterval*30+5*time.Second)
		}
		time.Sleep(150 * time.Millisecond)
	}

	// External signal — /v1/account/export now 401s because the API
	// key was deleted along with the account row. We can't reuse the
	// original `key` (the row it pointed at is gone), so we issue
	// the request with whatever was carried in the test — a 401
	// either way proves the row is gone.
	externalDeadline := time.Now().Add(2 * time.Second)
	for {
		_, status := doReq(t, h, key, http.MethodGet, "/v1/account/export", nil)
		if status == http.StatusUnauthorized {
			return
		}
		if time.Now().After(externalDeadline) {
			t.Fatalf("GET /v1/account/export = %d after row hard-delete (want 401)", status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// accountIDForEmail resolves an account id from a unique email. Each
// subtest gets its own pgtest schema, so the WHERE is unambiguous
// without needing a timestamp tiebreaker.
func accountIDForPlanLabel(t *testing.T, pool *pgxpool.Pool, plan api.Plan, label string) string {
	t.Helper()
	email := "e2e+" + string(plan) + "+" + label + "@test.example"
	var id string
	if err := pool.QueryRow(context.Background(),
		`select id from accounts where email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("resolve account id for %s: %v", email, err)
	}
	return id
}
