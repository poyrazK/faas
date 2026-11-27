package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// RunReadOnlyStatement runs req.SQL on conn, which a provider opened as the
// database's agent query login, inside a READ ONLY transaction with the
// query timeouts, reads at most req.MaxRows rows and req.MaxBytes of values
// in text form, and rolls back. The extended protocol it uses refuses more
// than one statement. A PostgreSQL error is returned as a *QueryError.
func RunReadOnlyStatement(ctx context.Context, conn *pgx.Conn, req ReadOnlyQueryRequest) (ReadOnlyQueryResult, error) {
	if req.MaxRows < 1 || req.MaxRows > QueryMaxRows || req.MaxBytes < 1 {
		return ReadOnlyQueryResult{}, ErrInvalid
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ReadOnlyQueryResult{}, ErrUnavailable
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, setting := range []string{
		"SET LOCAL statement_timeout = " + strconv.FormatInt(QueryStatementTimeout.Milliseconds(), 10),
		"SET LOCAL lock_timeout = " + strconv.FormatInt(QueryLockTimeout.Milliseconds(), 10),
		"SET LOCAL idle_in_transaction_session_timeout = " + strconv.FormatInt(QueryIdleInTransaction.Milliseconds(), 10),
	} {
		if _, err := tx.Exec(ctx, setting); err != nil {
			return ReadOnlyQueryResult{}, ErrUnavailable
		}
	}
	// The platform catalog query must not resolve operators or aggregates
	// from a customer-controlled search_path. Keep arbitrary user queries'
	// existing search_path behavior unchanged.
	if req.SQL == api.DataAPICatalogPreviewSQL {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path = pg_catalog"); err != nil {
			return ReadOnlyQueryResult{}, ErrUnavailable
		}
	}
	// SET takes no snapshot, so until a query runs the statement could still
	// switch the transaction to READ WRITE. A first query pins READ ONLY:
	// PostgreSQL refuses the switch once a snapshot exists.
	if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
		return ReadOnlyQueryResult{}, ErrUnavailable
	}
	// Describe-exec sends the statement with Parse/Bind/Execute: one
	// statement only, no plan cache left behind, every column in text form.
	rows, err := tx.Query(ctx, req.SQL, pgx.QueryExecModeDescribeExec, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		return ReadOnlyQueryResult{}, queryError(err)
	}
	defer rows.Close()
	var result ReadOnlyQueryResult
	types := conn.TypeMap()
	for _, field := range rows.FieldDescriptions() {
		name := "oid:" + strconv.FormatUint(uint64(field.DataTypeOID), 10)
		if t, ok := types.TypeForOID(field.DataTypeOID); ok {
			name = t.Name
		}
		result.Columns = append(result.Columns, QueryColumn{Name: field.Name, Type: name})
	}
	result.Rows = [][]*string{}
	bytes := 0
	for rows.Next() {
		if len(result.Rows) == req.MaxRows {
			result.Truncated = true
			break
		}
		raw := rows.RawValues()
		row := make([]*string, len(raw))
		size := 0
		for i, v := range raw {
			if v != nil {
				s := string(v)
				row[i] = &s
				size += len(v)
			}
		}
		if bytes+size > req.MaxBytes {
			result.Truncated = true
			break
		}
		bytes += size
		result.Rows = append(result.Rows, row)
	}
	if result.Truncated {
		// End the statement once a cap is reached: closing rows would
		// otherwise read the rest of the result and hold the database's
		// query slot until statement_timeout. A cancel request keeps the
		// connection usable; the statement ends with 57014, which a
		// truncated result ignores.
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = conn.PgConn().CancelRequest(cancelCtx)
		cancel()
	}
	rows.Close()
	if err := rows.Err(); err != nil && !result.Truncated {
		return ReadOnlyQueryResult{}, queryError(err)
	}
	result.RowCount = len(result.Rows)
	return result, nil
}

// queryError keeps what PostgreSQL said about the statement and hides
// everything else, such as a dropped connection.
func queryError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		message := pgErr.Message
		if pgErr.Hint != "" {
			message += "; hint: " + pgErr.Hint
		}
		if pgErr.Position > 0 {
			message += fmt.Sprintf(" (at character %d)", pgErr.Position)
		}
		return &QueryError{SQLState: pgErr.Code, Message: message}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &QueryError{SQLState: "57014", Message: "canceling statement due to timeout"}
	}
	return ErrUnavailable
}
