//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func directoryPGTargetFixture(t *testing.T) (*pgxpool.Pool, *PgStore, string, App, Cell, Binding) {
	t.Helper()
	pool := directoryPG(t)
	store := NewPgStore(pool)
	owner := directoryAccount(t, pool)
	app, err := store.CreateApp(t.Context(), owner, "global-shop", 3)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := store.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://api.fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-shop"}
	return pool, store, owner, app, cell, binding
}

func waitDirectoryLock(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%global_directory_cells%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("directory writer did not reach the expected database lock")
}

func beginDirectoryTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func TestGlobalDirectoryPostgresRegistryWinsAttachmentRace(t *testing.T) {
	pool, store, owner, app, cell, binding := directoryPGTargetFixture(t)
	tx := beginDirectoryTx(t, pool)
	if _, err := tx.Exec(t.Context(), `UPDATE global_directory_cells SET enabled=false WHERE id=$1`, cell.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := store.AttachTarget(ctx, owner, app.Slug, binding); result <- err }()
	waitDirectoryLock(t, pool)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrStaleCell) {
		t.Fatalf("stale probe committed: %v", err)
	}
	var targets, mappings int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM global_app_targets), (SELECT count(*) FROM global_account_cell_bindings)`).Scan(&targets, &mappings); err != nil {
		t.Fatal(err)
	}
	if targets != 0 || mappings != 0 {
		t.Fatalf("partial attachment: targets=%d mappings=%d", targets, mappings)
	}
}

func TestGlobalDirectoryPostgresAttachmentFencesRegistry(t *testing.T) {
	pool, store, owner, app, cell, binding := directoryPGTargetFixture(t)
	tx := beginDirectoryTx(t, pool)
	// Exercise the row guard even for a SQL caller bypassing the adapter.
	if _, err := tx.Exec(t.Context(), `INSERT INTO global_account_cell_bindings(account_id,cell_id,regional_account_id) VALUES($1,$2,$3)`, owner, cell.ID, binding.RegionalAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO global_app_targets(global_app_id,account_id,cell_id,region,cell_generation,regional_account_id,regional_app_id,regional_app_slug) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, app.ID, owner, cell.ID, cell.Region, cell.Generation, binding.RegionalAccountID, binding.RegionalAppID, binding.RegionalAppSlug); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		cell.APIBase = "https://new.fsn.example.com"
		_, err := store.PutCell(ctx, cell)
		result <- err
	}()
	waitDirectoryLock(t, pool)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(t.Context(), store, owner, app.Slug, cell.Region); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("changed endpoint resolved old proof: %v", err)
	}
}

func TestGlobalDirectoryPostgresSQLIdentityGuards(t *testing.T) {
	pool, store, owner, app, cell, binding := directoryPGTargetFixture(t)
	if _, err := store.AttachTarget(t.Context(), owner, app.Slug, binding); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql, constraint string
		args                  []any
	}{
		{"cell_region", `UPDATE global_directory_cells SET region='eu-hel' WHERE id=$1`, "global_directory_identity", []any{cell.ID}},
		{"global_owner", `UPDATE global_apps SET account_id=$1 WHERE id=$2`, "global_directory_identity", []any{uuid.NewString(), app.ID}},
		{"global_slug", `UPDATE global_apps SET slug='other-global' WHERE id=$1`, "global_directory_identity", []any{app.ID}},
		{"mapping", `UPDATE global_account_cell_bindings SET regional_account_id=$1 WHERE account_id=$2`, "global_directory_identity", []any{uuid.NewString(), owner}},
		{"local_app", `UPDATE global_app_targets SET regional_app_id=$1 WHERE global_app_id=$2`, "global_directory_identity", []any{uuid.NewString(), app.ID}},
		{"stale_target", `UPDATE global_app_targets SET cell_generation=7 WHERE global_app_id=$1`, "global_directory_stale_cell", []any{app.ID}},
		{"cell_init", `INSERT INTO control_plane_cells(singleton,id,compute_region) VALUES(true,$1,'eu-hel')`, "global_directory_scope", []any{uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), tc.sql, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("SQL bypass: %v", err)
			}
		})
	}
	// A direct generation edit cannot manufacture new or roll back old proofs.
	if _, err := pool.Exec(t.Context(), `UPDATE global_directory_cells SET generation=77 WHERE id=$1`, cell.ID); err != nil {
		t.Fatal(err)
	}
	actual, err := store.CellByRegion(t.Context(), cell.Region)
	if err != nil || actual.Generation != cell.Generation {
		t.Fatalf("generation bypass: %+v %v", actual, err)
	}
}
