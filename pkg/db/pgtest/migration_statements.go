package pgtest

import (
	"context"
	"strings"
	"testing"

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
	defer func() { _, _ = conn.Exec(context.Background(), "RESET lock_timeout") }()
	for _, statement := range MigrationStatements(raw, up) {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
