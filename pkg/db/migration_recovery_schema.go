package db

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Administrative recovery runs locally. Credentials are passed through the
// child environment, never a command argument or a diagnostic. The snapshot
// is exported by the caller's still-open transaction.
func migrationRecoverySchema(ctx context.Context, cfg *pgx.ConnConfig, snapshot string) ([]byte, error) {
	if !migrationRecoveryLocalConfig(cfg) {
		return nil, ErrMigrationRecoveryTransport
	}
	cmd := exec.CommandContext(ctx, "pg_dump", "-s", "--no-owner", "--no-privileges", "--no-sync", "--no-tablespaces", "--snapshot="+snapshot)
	cmd.Env = migrationRecoveryDumpEnv(cfg)
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("canonical schema verification with pg_dump: %w", err)
	}
	return stripMigrationRecoveryNoise(raw), nil
}

func migrationRecoveryLocalConfig(cfg *pgx.ConnConfig) bool {
	return cfg != nil && strings.HasPrefix(cfg.Host, "/") && cfg.TLSConfig == nil
}

func migrationRecoveryDumpEnv(cfg *pgx.ConnConfig) []string {
	env := make([]string, 0, len(os.Environ())+6)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PG") {
			env = append(env, value)
		}
	}
	return append(env, "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGDATABASE="+cfg.Database, "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGTZ=UTC")
}

func stripMigrationRecoveryNoise(raw []byte) []byte {
	for _, pattern := range []string{
		`(?m)^\\restrict [^\n]*\n?`, `(?m)^\\unrestrict [^\n]*\n?`,
		`(?m)^-- Dumped from database version [^\n]*\n?`,
		`(?m)^-- Dumped by pg_dump version [^\n]*\n?`,
		`(?m)^-- PostgreSQL database dump complete[^\n]*\n?`,
	} {
		raw = regexp.MustCompile(pattern).ReplaceAll(raw, nil)
	}
	return raw
}
