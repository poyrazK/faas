package pgpoolbounds

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// A statement Postgres refuses for missing privileges (SQLSTATE 42501) is
// counted on every hook it can surface through, wrapped or not; any other
// error, and success, is not (ADR-20261023104734 step 3).
func TestTracerCountsPermissionDenied(t *testing.T) {
	ctx := context.Background()
	denied := &pgconn.PgError{Code: "42501", Message: "permission denied for table apps"}
	other := &pgconn.PgError{Code: "23505", Message: "duplicate key"}
	var tr Tracer
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: denied})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: fmt.Errorf("read app: %w", denied)})
	tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{Err: denied})
	tr.TraceCopyFromEnd(ctx, nil, pgx.TraceCopyFromEndData{Err: denied})
	tr.TracePrepareEnd(ctx, nil, pgx.TracePrepareEndData{Err: denied})
	// Not counted: success, other SQLSTATEs, non-Postgres errors, and the
	// batch end that repeats a batch statement's error.
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: other})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("conn closed")})
	tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{Err: denied})
	if got := tr.PermissionDenied(); got != 5 {
		t.Fatalf("PermissionDenied = %d, want 5", got)
	}
}
