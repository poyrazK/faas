package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const cellUsage = "usage: gregalectl cell init|status --manifest-file PATH (explicit DATABASE_URL required; init requires a fresh migrated database)"

func cmdCellDispatch(args []string) int {
	if len(args) == 0 || args[0] == flagHelpLong || args[0] == flagHelpShort {
		_, _ = fmt.Fprintln(osStderr, cellUsage)
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if args[0] != "init" && args[0] != "status" {
		_, _ = fmt.Fprintln(osStderr, cellUsage)
		return 1
	}
	fs := flag.NewFlagSet("cell "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	manifestFile := fs.String("manifest-file", "", "regional deployment manifest")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *manifestFile == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, cellUsage)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	identity, err := operateControlPlaneCell(ctx, *manifestFile, args[0] == "init")
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl cell: %v\n", err)
		return 3
	}
	if jsonEnabled() {
		return emitOperatorJSON(identity)
	}
	if _, err := fmt.Fprintf(osStdout, "cell %s\tregion %s\n", identity.ID, identity.Region); err != nil {
		return 3
	}
	return 0
}

func operateControlPlaneCell(ctx context.Context, manifestFile string, initialize bool) (db.CellIdentity, error) {
	m, err := manifest.Load(manifestFile)
	if err != nil {
		return db.CellIdentity{}, fmt.Errorf("load cell manifest: %w", err)
	}
	if errs := m.Validate(); errs != nil {
		return db.CellIdentity{}, fmt.Errorf("validate cell manifest: %w", errs)
	}
	if m.ControlPlaneCell == nil {
		return db.CellIdentity{}, errors.New("manifest requires control_plane_cell")
	}
	expected := db.CellIdentity{ID: m.ControlPlaneCell.ID, Region: m.ControlPlaneCell.Region}
	pool, err := openCellOperatorPool(ctx)
	if err != nil {
		return db.CellIdentity{}, err
	}
	defer pool.Close()
	queries := &sqlc.Queries{}
	if initialize {
		var id pgtype.UUID
		if err := id.Scan(expected.ID); err != nil {
			return db.CellIdentity{}, fmt.Errorf("parse cell UUID: %w", err)
		}
		if _, err := queries.InitializeControlPlaneCell(ctx, pool, sqlc.InitializeControlPlaneCellParams{CellID: id, Region: expected.Region}); err != nil {
			return db.CellIdentity{}, fmt.Errorf("initialize fresh migrated cell: %w", err)
		}
	}
	actual, err := queries.ReadControlPlaneCell(ctx, pool)
	if err != nil {
		return db.CellIdentity{}, fmt.Errorf("read cell identity: %w", err)
	}
	if actual.CellID != expected.ID || actual.ComputeRegion != expected.Region {
		return db.CellIdentity{}, db.ErrControlPlaneCell
	}
	return expected, nil
}

// Only this explicit provisioning command opens without the daemon guard.
// No default production DSN or credential-bearing flag is accepted.
func openCellOperatorPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("explicit DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// pgconn.ParseConfigError includes its credential-bearing ConnString.
		return nil, errors.New("invalid DATABASE_URL")
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = "gregalectl-cell"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open cell operator database: %w", err)
	}
	return pool, nil
}
