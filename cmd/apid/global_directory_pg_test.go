//go:build !no_pg

package main

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestGlobalDirectoryStartupScope(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	srv := &server{}
	if err := srv.configureGlobalDirectory(t.Context(), false, nil); err != nil || srv.globalDirectory != nil {
		t.Fatal("default enabled the directory", err)
	}
	if err := srv.configureGlobalDirectory(t.Context(), true, nil); err == nil {
		t.Fatal("missing database pool accepted")
	}
	if err := srv.configureGlobalDirectory(t.Context(), true, pool); err != nil || srv.globalDirectory == nil {
		t.Fatal("unbound directory refused", err)
	}
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	regional := &server{}
	if err := regional.configureGlobalDirectory(t.Context(), true, pool); !errors.Is(err, globaldirectory.ErrBoundCell) || regional.globalDirectory != nil {
		t.Fatal("regional database accepted global directory", err)
	}
}
