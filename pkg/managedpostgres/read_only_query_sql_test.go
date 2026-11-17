// adr: 20261117215858
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRunReadOnlyStatementAgainstPostgres(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `CREATE TABLE agent_query_rows (id int PRIMARY KEY, note text); INSERT INTO agent_query_rows SELECT g, CASE WHEN g = 2 THEN NULL ELSE 'row ' || g END FROM generate_series(1, 5) g`); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	run := func(sql string, maxRows, maxBytes int) (ReadOnlyQueryResult, error) {
		return RunReadOnlyStatement(ctx, conn, ReadOnlyQueryRequest{SQL: sql, MaxRows: maxRows, MaxBytes: maxBytes})
	}

	got, err := run(`SELECT id, note FROM agent_query_rows ORDER BY id`, 3, QueryMaxValueBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got.RowCount != 3 || !got.Truncated || len(got.Columns) != 2 || got.Columns[0].Type != "int4" || *got.Rows[0][0] != "1" || got.Rows[1][1] != nil {
		t.Fatalf("result = %+v", got)
	}
	if got, err = run(`SELECT note FROM agent_query_rows ORDER BY id`, 10, 10); err != nil || got.RowCount != 3 || !got.Truncated { // "row 1", NULL, "row 3" fill 10 bytes
		t.Fatalf("byte cap: %+v %v", got, err)
	}

	var queryErr *QueryError
	for sql, state := range map[string]string{
		`DELETE FROM agent_query_rows`:                            "25006", // read-only transaction
		`CREATE TABLE agent_query_new (id int)`:                   "25006",
		`SELECT 1; DELETE FROM agent_query_rows`:                  "42601", // one statement only
		`SELECT pg_sleep(10)`:                                     "57014", // statement timeout
		`SELECT missing_column FROM agent_query_rows`:             "42703",
		`SET TRANSACTION READ WRITE`:                              "25001",
		`SELECT set_config('transaction_read_only', 'off', true)`: "25001",
	} {
		_, err := run(sql, 10, QueryMaxValueBytes)
		if !errors.As(err, &queryErr) || queryErr.SQLState != state {
			t.Errorf("%s: err = %v, want SQLSTATE %s", sql, err, state)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_query_rows`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("rows after refused writes = %d %v", count, err)
	}
	// The connection is still usable after errors, and nothing leaked out of
	// the rolled-back transactions.
	var timeout, readOnly string
	if err := conn.QueryRow(ctx, `SELECT current_setting('statement_timeout'), current_setting('transaction_read_only')`).Scan(&timeout, &readOnly); err != nil || timeout != "0" || readOnly != "off" {
		t.Fatalf("session settings leaked: timeout=%q read_only=%q %v", timeout, readOnly, err)
	}
}

func TestValidateQuerySQL(t *testing.T) {
	for _, sql := range []string{"", "   ", "select 1\x00", strings.Repeat("x", QueryMaxSQLBytes+1)} {
		if ValidateQuerySQL(sql) == nil {
			t.Errorf("accepted %q", sql[:min(len(sql), 20)])
		}
	}
	if err := ValidateQuerySQL("select 1"); err != nil {
		t.Fatal(err)
	}
}
