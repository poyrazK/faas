//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func requireRegionConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != constraint {
		t.Fatalf("error = %v, want check violation %s", err, constraint)
	}
}

func TestComputeRegionPostgresDirectWrites(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	node := fx.Node
	region := "eu-fsn"
	node.Region = &region
	if _, err := store.UpsertComputeNodeFromOperator(fx.Ctx, node); err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "regional-"+uuid.NewString(), "", region
	app, err := store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	// The idle peer is protected as soon as the region is referenced, even
	// before ownership is claimed or a migration creates its destination VM.
	_, err = pool.Exec(fx.Ctx, "UPDATE compute_nodes SET region='eu-hel' WHERE id=$1", node.ID)
	requireRegionConstraint(t, err, "compute_nodes_region_in_use")
	_, err = pool.Exec(fx.Ctx, "UPDATE apps SET compute_region='' WHERE id=$1", app.ID)
	requireRegionConstraint(t, err, "apps_compute_region_immutable")
	other, err := store.ComputeNodeByName(fx.Ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(fx.Ctx, "UPDATE apps SET node_id=$2 WHERE id=$1", app.ID, other.ID)
	requireRegionConstraint(t, err, "apps_compute_region_owner")
	_, err = pool.Exec(fx.Ctx, "INSERT INTO instances (app_id,node_id,state,ram_mb) VALUES ($1,$2,'running',128)", app.ID, other.ID)
	requireRegionConstraint(t, err, "instances_compute_region")
	if err := store.SetAppNodeID(fx.Ctx, app.ID, node.ID); err != nil {
		t.Fatal(err)
	}
}

func TestComputeRegionPostgresAdmissionSerializesWithRelabel(t *testing.T) {
	for _, admissionFirst := range []bool{true, false} {
		name := "relabel_first"
		if admissionFirst {
			name = "admission_first"
		}
		t.Run(name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			store := state.NewPgStore(pool)
			fx := conformance.Seed(t, store)
			node := fx.Node
			region := "eu-fsn"
			node.Region = &region
			if _, err := store.UpsertComputeNodeFromOperator(fx.Ctx, node); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(context.Background()) }()
			admissionSQL := "/* compute-region-race */ INSERT INTO apps (account_id,slug,type,ram_mb,compute_region,node_id) VALUES ($1,$2,'app',128,'eu-fsn',$3)"
			relabelSQL := "/* compute-region-race */ UPDATE compute_nodes SET region='eu-hel' WHERE id=$1"
			args := []any{fx.Account.ID, "regional-" + uuid.NewString(), node.ID}
			firstSQL, secondSQL := admissionSQL, relabelSQL
			firstArgs, secondArgs := args, []any{node.ID}
			constraint := "compute_nodes_region_in_use"
			if !admissionFirst {
				firstSQL, secondSQL = secondSQL, firstSQL
				firstArgs, secondArgs = secondArgs, firstArgs
				constraint = "apps_compute_region_owner"
			}
			if _, err := writer.Exec(ctx, firstSQL, firstArgs...); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := pool.Exec(ctx, secondSQL, secondArgs...)
				done <- err
			}()
			// Observe the real row-lock wait before releasing the writer.
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '/* compute-region-race */%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("operation did not wait for writer: %v", err)
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
				requireRegionConstraint(t, err, constraint)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
