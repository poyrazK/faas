//go:build !no_pg

package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestComputeRegionRegistryPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	t.Run("backfill_includes_inactive_nodes_and_app_tombstones", func(t *testing.T) {
		label := "eu-backfill"
		node := fx.Node
		node.ID, node.Name, node.Region, node.Active = "", "backfill-"+uuid.NewString(), &label, false
		if _, err := store.CreateComputeNode(fx.Ctx, node); err != nil {
			t.Fatal(err)
		}
		sql, err := migrations.FS.ReadFile("20261001203000000_compute_region_registry.sql")
		if err != nil {
			t.Fatal(err)
		}
		// Exercise the shipped migration statement, including its canonical filter.
		text := string(sql)
		start := strings.Index(text, "INSERT INTO compute_regions")
		end := strings.Index(text[start:], ";") + start + 1
		tombstoneRegion := state.ComputeRegion{ID: "eu-tombstone", DisplayName: "Tombstone", AcceptingNewApps: true}
		if _, err := store.UpsertComputeRegion(fx.Ctx, tombstoneRegion); err != nil {
			t.Fatal(err)
		}
		app := fx.App
		app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "old-"+uuid.NewString(), "", tombstoneRegion.ID
		app, err = store.CreateApp(fx.Ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SoftDeleteAppCascade(fx.Ctx, app.ID); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(fx.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		// Simulate the pre-registry catalog inside a rolled-back transaction.
		// The node and tombstone remain real legacy inputs to the shipped SQL.
		if _, err := tx.Exec(fx.Ctx, "ALTER TABLE compute_regions DISABLE TRIGGER compute_regions_identity_guard"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(fx.Ctx, "DELETE FROM compute_regions"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(fx.Ctx, text[start:end]); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{label, tombstoneRegion.ID, "local"} {
			var accepting bool
			if err := tx.QueryRow(fx.Ctx, "SELECT accepting_new_apps FROM compute_regions WHERE id=$1", id).Scan(&accepting); err != nil || !accepting {
				t.Fatalf("legacy region %s missing: %v", id, err)
			}
		}
		if err := tx.Rollback(fx.Ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: label, DisplayName: label, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		got, err := store.ComputeRegionByID(fx.Ctx, label)
		if err != nil || !got.AcceptingNewApps {
			t.Fatalf("inactive region vanished: %+v %v", got, err)
		}
		_, err = pool.Exec(fx.Ctx, "UPDATE compute_regions SET id='replacement' WHERE id=$1", label)
		requireRegionConstraint(t, err, "compute_region_identity")
		_, err = pool.Exec(fx.Ctx, "DELETE FROM compute_regions WHERE id=$1", label)
		requireRegionConstraint(t, err, "compute_region_in_use")
	})
	for _, admissionFirst := range []bool{false, true} {
		name := "pause_first"
		if admissionFirst {
			name = "admission_first"
		}
		t.Run(name, func(t *testing.T) {
			id := "eu-race-" + uuid.NewString()
			if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: id, DisplayName: "Race region", AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(context.Background()) }()
			insert := "/* registry-admission-race */ INSERT INTO apps (account_id,slug,type,ram_mb,compute_region) VALUES ($1,$2,'app',128,$3)"
			pause := "/* registry-admission-race */ UPDATE compute_regions SET accepting_new_apps=false WHERE id=$1"
			insertArgs := []any{fx.Account.ID, "region-" + uuid.NewString(), id}
			first, second := pause, insert
			firstArgs, secondArgs := []any{id}, insertArgs
			if admissionFirst {
				first, second, firstArgs, secondArgs = insert, pause, insertArgs, []any{id}
			}
			if _, err := writer.Exec(ctx, first, firstArgs...); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := pool.Exec(ctx, second, secondArgs...); done <- err }()
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '/* registry-admission-race */%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("operation did not serialize: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := writer.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if admissionFirst {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					requireRegionConstraint(t, err, "compute_region_admission_paused")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Direct SQL cannot bypass the now-paused policy.
			_, err = pool.Exec(ctx, insert, fx.Account.ID, "denied-"+uuid.NewString(), id)
			requireRegionConstraint(t, err, "compute_region_admission_paused")
		})
	}
}
