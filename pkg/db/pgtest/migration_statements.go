package pgtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationStatements splits one direction of a goose SQL migration the way
// goose does: a statement ends at a line ending in a semicolon, except inside
// `-- +goose StatementBegin` / `StatementEnd`.
func MigrationStatements(raw string, up bool) []string {
	var out []string
	var buf strings.Builder
	want := "down"
	if up {
		want = "up"
	}
	section, inBlock := "", false
	flush := func() {
		if strings.TrimSpace(buf.String()) != "" {
			out = append(out, buf.String())
		}
		buf.Reset()
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "-- +goose Up"):
			section = "up"
			continue
		case strings.HasPrefix(trimmed, "-- +goose Down"):
			flush()
			section = "down"
			continue
		case strings.HasPrefix(trimmed, "-- +goose StatementBegin"):
			inBlock = true
			continue
		case strings.HasPrefix(trimmed, "-- +goose StatementEnd"):
			inBlock = false
			flush()
			continue
		case strings.HasPrefix(trimmed, "-- +goose"):
			continue
		}
		if section != want {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		if !inBlock && !strings.HasPrefix(trimmed, "--") && strings.HasSuffix(trimmed, ";") {
			flush()
		}
	}
	flush()
	return out
}

// ExecMigration runs one direction of a goose SQL migration statement by
// statement on a single connection, as goose runs a `NO TRANSACTION` file.
// Each statement runs on its own, so a DO block may COMMIT.
func ExecMigration(ctx context.Context, t *testing.T, pool *pgxpool.Pool, raw string, up bool) error {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// A failed run must not leave its session lock_timeout on a pooled connection.
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "RESET lock_timeout") }()
	// Tests in other schemas of the same database apply these files at the
	// same time; a migration that looks indexes up by name in the catalogue
	// (an invalid concurrent build, say) would see theirs. One at a time: the
	// lock is polled rather than waited on, because CREATE INDEX
	// CONCURRENTLY waits for every open snapshot, a blocked lock call's too.
	if err := lockMigrations(ctx, conn); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()
	for _, statement := range MigrationStatements(raw, up) {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// migrationLockKey serializes ExecMigration across a database's test schemas.
const migrationLockKey int64 = 0x7067746573746d67 // "pgtestmg"

// lockMigrations takes migrationLockKey on conn, polling so that no
// statement waits while another test's concurrent index build runs.
func lockMigrations(ctx context.Context, conn *pgxpool.Conn) error {
	for {
		var ok bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", migrationLockKey).Scan(&ok); err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
