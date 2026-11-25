// adr: 20261023181931 — amendment 3: a switch that keeps its release records no undone deployment.
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDatabaseSwitchThatKeepsItsReleaseRoundTrips(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		switches := s.(state.DatabaseSwitchStore)
		now := time.Now().UTC()
		base := state.DatabaseSwitch{AccountID: a.ID, AppID: app.ID, Scope: "default", SourceDatabaseID: uuid.NewString(),
			UndoneDeploymentIDs: []string{}, TargetName: "orders-0900", RestorePoint: now.Add(-time.Hour), CreatedAt: now}

		// Undoing nothing while changing the release is not a switch.
		moving := base
		moving.FromDeploymentID, moving.TargetDeploymentID = current.ID, target.ID
		if _, err := switches.CreateDatabaseSwitch(t.Context(), moving); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("switch undoing nothing to another release: %v, want ErrInvalidArgument", err)
		}

		keep := base
		keep.FromDeploymentID, keep.TargetDeploymentID = current.ID, current.ID
		created, err := switches.CreateDatabaseSwitch(t.Context(), keep)
		if err != nil {
			t.Fatal(err)
		}
		got, err := switches.DatabaseSwitch(t.Context(), a.ID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.KeepsRelease() || len(got.UndoneDeploymentIDs) != 0 || got.TargetDeploymentID != current.ID || got.Phase != state.DatabaseSwitchRestoring {
			t.Fatalf("read back %+v", got)
		}
		undone, err := switches.ReleasesUndoneByDatabaseSwitches(t.Context(), app.ID, "default")
		if err != nil || len(undone) != 0 {
			t.Fatalf("a switch that keeps its release undid %v (%v)", undone, err)
		}
	})
}

func TestPgMigrationSwitchLeasesExcludeLegacyWorkers(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	account, app, deploy := seedLiveDeploy(t, store, ctx, "migration-switch", "migration-switch")
	migration, err := store.CreateDatabaseSwitch(ctx, state.DatabaseSwitch{
		ID: uuid.NewString(), AccountID: account, AppID: app, Scope: "default",
		FromDeploymentID: deploy, TargetDeploymentID: deploy, SourceDatabaseID: uuid.NewString(),
		TargetDatabaseID: uuid.NewString(), TargetName: "native-target", MigrationOperationID: uuid.NewString(), CreatedAt: now,
	})
	if err != nil || migration.LeaseUntil != nil || migration.LeaseToken != "" {
		t.Fatalf("create migration switch: %+v, %v", migration, err)
	}
	legacyAccount, legacyApp, legacyDeploy := seedLiveDeploy(t, store, ctx, "restore-switch", "restore-switch")
	restore, err := store.CreateDatabaseSwitch(ctx, state.DatabaseSwitch{
		AccountID: legacyAccount, AppID: legacyApp, Scope: "default", FromDeploymentID: legacyDeploy, TargetDeploymentID: legacyDeploy,
		SourceDatabaseID: uuid.NewString(), TargetName: "restored-target", RestorePoint: now.Add(-time.Hour), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the prior apid's unmodified phase and lease predicate.
	legacyClaim := func(at time.Time) []string {
		t.Helper()
		rows, err := pool.Query(ctx, `UPDATE release_database_switches sw
		   SET lease_token = 'old-worker', lease_until = $1, updated_at = $2
		  FROM (
		    SELECT id FROM release_database_switches
		     WHERE phase NOT IN ('completed', 'failed', 'cancelled')
		       AND retry_at <= $2 AND (lease_until IS NULL OR lease_until <= $2)
		     ORDER BY retry_at, id LIMIT 10 FOR UPDATE SKIP LOCKED
		  ) due WHERE sw.id = due.id RETURNING sw.id::text`, at.Add(time.Hour), at)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if ids := legacyClaim(now); len(ids) != 1 || ids[0] != restore.ID {
		t.Fatalf("legacy worker claimed %v, want restore %s only", ids, restore.ID)
	}
	for _, phase := range []string{state.DatabaseSwitchSwitching, state.DatabaseSwitchResuming} {
		claimed, err := store.ClaimDueDatabaseSwitches(ctx, "new-worker", now, now.Add(time.Second), 10)
		if err != nil || len(claimed) != 1 || claimed[0].ID != migration.ID || claimed[0].LeaseUntil == nil ||
			claimed[0].LeaseToken != "new-worker" || !claimed[0].LeaseUntil.Equal(now.Add(time.Second)) {
			t.Fatalf("migration worker claimed %+v, %v", claimed, err)
		}
		if duplicate, err := store.ClaimDueDatabaseSwitches(ctx, "another-worker", now, now.Add(time.Second), 10); err != nil || len(duplicate) != 0 {
			t.Fatalf("claimed an active lease: %+v, %v", duplicate, err)
		}
		next := claimed[0]
		next.Phase = phase
		saved, err := store.SaveDatabaseSwitch(ctx, next, now)
		if err != nil || saved.Phase != phase || saved.LeaseUntil != nil || saved.LeaseToken != "" {
			t.Fatalf("save %s: %+v, %v", phase, saved, err)
		}
		if ids := legacyClaim(now); len(ids) != 0 {
			t.Fatalf("legacy worker claimed %v in %s", ids, phase)
		}
	}
}
