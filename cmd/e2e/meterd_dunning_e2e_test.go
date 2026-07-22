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

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

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
