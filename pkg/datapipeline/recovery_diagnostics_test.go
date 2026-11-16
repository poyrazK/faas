package datapipeline

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Record only Go operation names and database error identifiers. SQL text,
// arguments, connection strings and server error details can contain secrets.
type nativeRecoveryQueryTrace struct {
	last []string
}

func (trace *nativeRecoveryQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(data.SQL, "rollback") || strings.Contains(data.SQL, "pg_advisory_unlock") {
		return ctx
	}
	trace.last = nil
	for _, line := range strings.Split(string(debug.Stack()), "\n") {
		if !strings.HasPrefix(line, "github.com/onebox-faas/faas/pkg/datapipeline.") || strings.Contains(line, "nativeRecoveryQueryTrace") {
			continue
		}
		line = strings.ReplaceAll(strings.ReplaceAll(line, "(*", ""), ").", ".")
		name, _, _ := strings.Cut(line, "(")
		trace.last = append(trace.last, strings.TrimPrefix(name, "github.com/onebox-faas/faas/pkg/datapipeline."))
		if len(trace.last) == 6 {
			break
		}
	}
	return ctx
}

func (*nativeRecoveryQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func traceNativeRecoveryTarget(t *testing.T, f *nativeRecoveryFixture) {
	t.Helper()
	trace := &nativeRecoveryQueryTrace{}
	config := f.target.Config()
	config.Tracer = trace
	target, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal("connect traced recovery target", err)
	}
	previous := f.target
	f.target = target
	if err = previous.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
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
		t.Logf("native recovery failure: type=%T class=%s sqlstate=%s constraint=%s table=%s routine=%s target_calls=%s", err, class, code, constraint, table, routine, strings.Join(trace.last, " -> "))
		return err
	}
}
