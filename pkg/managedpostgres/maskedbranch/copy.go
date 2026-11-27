package maskedbranch

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

const receiptTable = "gregale_masked_branch.copy_receipt"

// Copy loads and validates the projection in one destination transaction. A
// committed receipt makes a retry after a lost acknowledgement idempotent.
// The destination must be private to the operation until this returns success.
func Copy(ctx context.Context, source, target *pgx.Conn, operation string, key [32]byte, policy []api.ManagedPostgresMaskedTable) error {
	policy, err := Normalize(policy)
	if err != nil {
		return err
	}
	if operation == "" || key == ([32]byte{}) || source == nil || target == nil || source == target {
		return pgerrors.ErrInvalid
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return pgerrors.ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	dst, err := target.Begin(ctx)
	if err != nil {
		return unavailable(ctx, "start destination transaction")
	}
	defer rollback(ctx, dst)
	if _, err := dst.Exec(ctx, `SET LOCAL search_path = pg_catalog; SET LOCAL statement_timeout = '60s'; SET LOCAL lock_timeout = '3s';
		SELECT pg_catalog.pg_advisory_xact_lock(1913852337, 1327438751)`); err != nil {
		return unavailable(ctx, "lock destination")
	}
	var committed bool
	if err := dst.QueryRow(ctx, `SELECT pg_catalog.to_regclass($1) IS NOT NULL`, receiptTable).Scan(&committed); err != nil {
		return unavailable(ctx, "read destination receipt")
	}
	if committed {
		var storedID, storedPolicy string
		if err := dst.QueryRow(ctx, `SELECT operation, policy FROM `+receiptTable).Scan(&storedID, &storedPolicy); err != nil {
			return unavailable(ctx, "read destination receipt")
		}
		if storedID != operation || storedPolicy != fingerprint {
			return fmt.Errorf("%w: destination contains a different masked copy", pgerrors.ErrConflict)
		}
		return nil
	}
	var empty bool
	if err := dst.QueryRow(ctx, `SELECT NOT EXISTS (SELECT FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public')`).Scan(&empty); err != nil {
		return unavailable(ctx, "inspect destination")
	}
	if !empty {
		return fmt.Errorf("%w: masked branch destination is not empty", pgerrors.ErrConflict)
	}
	src, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return unavailable(ctx, "start source snapshot")
	}
	defer rollback(ctx, src)
	if _, err := src.Exec(ctx, `SET LOCAL search_path = pg_catalog; SET LOCAL statement_timeout = '60s';
		SET LOCAL lock_timeout = '3s'; SET LOCAL row_security = off`); err != nil {
		return unavailable(ctx, "configure source snapshot")
	}
	tables, err := readTables(ctx, src, policy)
	if err != nil {
		return err
	}
	keys, references, err := constraintsForTables(tables)
	if err != nil {
		return err
	}
	rowsLeft, bytesLeft := int64(MaxRows), int64(MaxBytes)
	for _, t := range tables {
		if _, err := dst.Exec(ctx, tableDDL(t)); err != nil {
			return destinationError(ctx, err)
		}
		if err := copyTable(ctx, src, dst, t, key, &rowsLeft, &bytesLeft); err != nil {
			return err
		}
		if err := resetSequences(ctx, dst, t); err != nil {
			return err
		}
	}
	for _, group := range [][]string{keys, references} {
		for _, statement := range group {
			if _, err := dst.Exec(ctx, statement); err != nil {
				return destinationError(ctx, err)
			}
		}
	}
	if _, err := dst.Exec(ctx, `CREATE SCHEMA gregale_masked_branch;
		CREATE TABLE `+receiptTable+` (operation text NOT NULL, policy text NOT NULL)`); err != nil {
		return unavailable(ctx, "create destination receipt")
	}
	if _, err := dst.Exec(ctx, `INSERT INTO `+receiptTable+` VALUES ($1, $2)`, operation, fingerprint); err != nil {
		return unavailable(ctx, "record destination receipt")
	}
	if err := dst.Commit(ctx); err != nil {
		return unavailable(ctx, "commit masked copy")
	}
	return nil
}

func constraintsForTables(tables []table) (keys, references []string, err error) {
	byName := make(map[string]table, len(tables))
	for _, t := range tables {
		byName[t.name] = t
	}
	for _, t := range tables {
		for _, c := range t.constraints {
			ddl, err := constraintDDL(t, c, byName)
			if err != nil {
				return nil, nil, err
			}
			if c.kind == "f" {
				references = append(references, ddl)
			} else {
				keys = append(keys, ddl)
			}
		}
	}
	return keys, references, nil
}

func copyTable(ctx context.Context, src, dst pgx.Tx, t table, key [32]byte, rowsLeft, bytesLeft *int64) error {
	names, projection, widths := tableProjection(t)
	rows, err := src.Query(ctx, "SELECT "+strings.Join(projection, ", ")+", "+strings.Join(widths, " + ")+
		" FROM ONLY "+publicTable(t.name).Sanitize()+" LIMIT $1", *rowsLeft+1)
	if err != nil {
		return unavailable(ctx, "read source rows")
	}
	defer rows.Close()
	var rowCount int64
	var copyErr error
	input := pgx.CopyFromFunc(func() ([]any, error) {
		if !rows.Next() {
			if rows.Err() != nil {
				copyErr = unavailable(ctx, "read source rows")
			}
			return nil, copyErr
		}
		if rowCount >= *rowsLeft {
			copyErr = pgerrors.ErrQuotaExceeded
			return nil, copyErr
		}
		values, err := rows.Values()
		if err != nil {
			copyErr = unavailable(ctx, "decode source row")
			return nil, copyErr
		}
		width, ok := values[len(t.columns)].(int64)
		if !ok || width > maxRowBytes {
			copyErr = pgerrors.ErrQuotaExceeded
			return nil, copyErr
		}
		values = values[:len(t.columns)]
		var size int64
		for i, raw := range rows.RawValues()[:len(t.columns)] {
			size += int64(len(raw))
			c := t.columns[i]
			if values[i] != nil && (c.action == "email" || c.action == "token") {
				v, ok := values[i].(string)
				if !ok {
					copyErr = unsupported(t.name+"."+c.name, "expected a text value")
					return nil, copyErr
				}
				masked := mask(key, c.action, v)
				values[i] = masked
				size += int64(len(masked))
			}
		}
		if size > *bytesLeft {
			copyErr = pgerrors.ErrQuotaExceeded
			return nil, copyErr
		}
		*bytesLeft -= size
		rowCount++
		return values, nil
	})
	copied, err := dst.CopyFrom(ctx, publicTable(t.name), names, input)
	if copyErr != nil {
		return copyErr
	}
	if err != nil {
		return destinationError(ctx, err)
	}
	if copied != rowCount {
		return fmt.Errorf("%w: destination row count differs", pgerrors.ErrConflict)
	}
	*rowsLeft -= copied
	return nil
}

func tableProjection(t table) (names, fields, widths []string) {
	widths = []string{"0::bigint"}
	columnLimit := strconv.Itoa(maxRowBytes / len(t.columns))
	for _, c := range t.columns {
		names = append(names, c.name)
		field := pgx.Identifier{c.name}.Sanitize()
		if c.action == "null" {
			fields = append(fields, "NULL::"+c.sqlType)
			continue
		}
		// Bound variable-width values before PostgreSQL sends them to the worker.
		switch {
		case c.sqlType == "pg_catalog.text", c.sqlType == "pg_catalog.bytea",
			strings.HasPrefix(c.sqlType, "pg_catalog.varchar"), strings.HasPrefix(c.sqlType, "pg_catalog.bpchar"):
			width := "pg_catalog.octet_length(" + field + ")"
			wide := width + " > " + columnLimit
			widths = append(widths, "(CASE WHEN "+wide+" THEN "+strconv.Itoa(maxRowBytes+1)+" ELSE COALESCE("+width+", 0) END)::bigint")
			field = "CASE WHEN NOT (" + wide + ") THEN " + field + " END"
		}
		fields = append(fields, field)
	}
	return names, fields, widths
}

func resetSequences(ctx context.Context, tx pgx.Tx, t table) error {
	for _, c := range t.columns {
		if c.identity == "" {
			continue
		}
		name := pgx.Identifier{c.name}.Sanitize()
		qualified := publicTable(t.name).Sanitize()
		_, err := tx.Exec(ctx, "SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence($1, $2), "+
			"GREATEST(COALESCE(max("+name+"), 1), 1), COALESCE(max("+name+"), 0) >= 1) FROM "+qualified, qualified, c.name)
		if err != nil {
			return destinationError(ctx, err)
		}
	}
	return nil
}

func mask(key [32]byte, action, raw string) string {
	hash := hmac.New(sha256.New, key[:])
	_, _ = hash.Write([]byte(action + "\x00"))
	_, _ = hash.Write([]byte(raw))
	value := hex.EncodeToString(hash.Sum(nil)[:16])
	if action == "email" {
		return value + "@example.test"
	}
	return value
}

func rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func unavailable(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// PostgreSQL and driver errors may include data, hostnames or credentials.
	return fmt.Errorf("%w: %s", pgerrors.ErrUnavailable, stage)
}

func destinationError(ctx context.Context, err error) error {
	var server *pgconn.PgError
	if errors.As(err, &server) && (strings.HasPrefix(server.Code, "22") || strings.HasPrefix(server.Code, "23")) {
		return fmt.Errorf("%w: masked rows do not satisfy the destination types or constraints", pgerrors.ErrConflict)
	}
	return unavailable(ctx, "write destination")
}
