package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationRecoveryDumpEnvExcludesAlternateCredentials(t *testing.T) {
	t.Setenv("PGSERVICE", "unreviewed-service")
	t.Setenv("PGPASSFILE", "/unreviewed/passwords")
	t.Setenv("PGPASSWORD", "inherited-secret")
	cfg := &pgx.ConnConfig{}
	cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.Password = "/local/socket", 5432, "reviewed", "owner", "selected-secret"
	env := migrationRecoveryDumpEnv(cfg)
	joined := strings.Join(env, "\n")
	for _, absent := range []string{"PGSERVICE=", "PGPASSFILE=", "inherited-secret"} {
		if strings.Contains(joined, absent) {
			t.Fatal("pg_dump can inherit unreviewed connection or credential inputs")
		}
	}
	if !strings.Contains(joined, "PGDATABASE=reviewed") || !strings.Contains(joined, "PGPASSWORD=selected-secret") {
		t.Fatal("pg_dump did not receive the selected database credentials")
	}
}

func TestMigrationRecoveryDumpFailureDoesNotExposeDiagnostic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("#!/bin/sh\nprintf 'credential-secret schema-secret' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	cfg := &pgx.ConnConfig{}
	cfg.Host = "/local/socket"
	_, err := migrationRecoverySchema(context.Background(), cfg, "reviewed-snapshot")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("failed dump exposed schema or credential diagnostic")
	}
}

func TestMigrationRecoveryDumpRefusesRemoteTransport(t *testing.T) {
	cfg := &pgx.ConnConfig{}
	cfg.Host = "unreviewed.example"
	if _, err := migrationRecoverySchema(context.Background(), cfg, "snapshot"); !errors.Is(err, ErrMigrationRecoveryTransport) {
		t.Fatalf("remote dump transport got %v", err)
	}
}

func TestApplicationStandardLedgerRecoveryRefusesRemoteBeforeOpening(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgresql://owner@unreviewed.example/reviewed?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	if err := PrepareApplicationStandardLedgerRecovery(ctx, pool); !errors.Is(err, ErrMigrationRecoveryTransport) {
		t.Fatalf("remote prepare reached database: %v", err)
	}
	if _, err := PreviewApplicationStandardLedgerRecovery(ctx, pool); !errors.Is(err, ErrMigrationRecoveryTransport) {
		t.Fatalf("remote preview reached database: %v", err)
	}
	if _, err := ApplyApplicationStandardLedgerRecovery(ctx, pool, strings.Repeat("0", 64)); !errors.Is(err, ErrMigrationRecoveryTransport) {
		t.Fatalf("remote apply reached database: %v", err)
	}
}
