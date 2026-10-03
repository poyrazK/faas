//go:build !no_pg

package globaldirectory

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func directoryPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func directoryAccount(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	account, err := state.NewPgStore(pool).CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return account.ID
}

func TestGlobalDirectoryPostgresContract(t *testing.T) {
	pool := directoryPG(t)
	directoryContract(t, NewPgStore(pool), directoryAccount(t, pool), directoryAccount(t, pool))
}

func TestGlobalDirectoryPostgresQuotaRace(t *testing.T) {
	pool := directoryPG(t)
	quotaRace(t, NewPgStore(pool), directoryAccount(t, pool))
}

func TestGlobalDirectoryRejectsRegionalDatabase(t *testing.T) {
	pool := directoryPG(t)
	_, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-fsn"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool)
	if err := store.CheckScope(t.Context()); !errors.Is(err, ErrBoundCell) {
		t.Fatal(err)
	}
	if _, err := store.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-hel", APIBase: "https://hel.example.com", Enabled: true}); !errors.Is(err, ErrBoundCell) {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), directoryAccount(t, pool), "global-shop", 3); !errors.Is(err, ErrBoundCell) {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), "INSERT INTO global_directory_cells(id,region,api_base,enabled) VALUES ($1,'eu-hel','https://hel.example.com',true)", uuid.NewString())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "global_directory_scope" {
		t.Fatalf("SQL scope bypass: %v", err)
	}
}

func TestGlobalDirectoryCannotBecomeRegionalCell(t *testing.T) {
	pool := directoryPG(t)
	store := NewPgStore(pool)
	if _, err := store.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-hel"})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "global_directory_scope" {
		t.Fatalf("rebind directory: %v", err)
	}
	if err := store.CheckScope(t.Context()); err != nil {
		t.Fatalf("partial cell initialization: %v", err)
	}
}
