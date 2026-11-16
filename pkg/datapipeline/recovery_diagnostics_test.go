package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Report only error classes and database identifiers from the original target
// session. Reconnecting here could hide session-local recovery failures.
func traceNativeRecoveryTarget(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	original := f.engine.Target
	f.engine.Target = func(ctx context.Context, account, database string, run func(*pgx.Conn, string) error) error {
		err := original(ctx, account, database, run)
		if err == nil {
			return nil
		}
		class := "other"
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			class = "row_not_found"
		case errors.Is(err, ErrConflict):
			class = "receipt_conflict"
		case errors.Is(err, ErrUnavailable):
			class = "unavailable"
		case errors.Is(err, context.DeadlineExceeded):
			class = "deadline"
		}
		var pg *pgconn.PgError
		var code, constraint, table, routine string
		if errors.As(err, &pg) {
			code, constraint, table, routine = pg.Code, pg.ConstraintName, pg.TableName, pg.Routine
		}
		t.Logf("native recovery failure: type=%T class=%s sqlstate=%s constraint=%s table=%s routine=%s", err, class, code, constraint, table, routine)
		return err
	}
}
