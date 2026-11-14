//go:build !no_pg

// adr: 1027 — fresh unnamed Parse replans after DDL.
package pgproxy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A new unnamed Parse must acquire a fresh row description after DDL.
func TestPooledUnnamedParseRefreshesAfterDDL(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			id := uuid.NewString()
			role, pw := h.addRole(t, id)
			table := "ddl_parse_" + uuid.NewString()[:8]
			if _, err := h.backend.Exec(ctx, "CREATE TABLE "+table+" (a int); INSERT INTO "+table+" VALUES (1); GRANT SELECT ON "+table+" TO "+role); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
			host := id
			if pooled {
				host += pooledSuffix
			}
			host += "." + testSuffix
			c, err := h.connect(ctx, host, role, pw, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close(context.Background()) }()
			sql := "SELECT * FROM " + table
			if _, err = c.PgConn().Prepare(ctx, "", sql, nil); err != nil {
				t.Fatal(err)
			}
			first := c.PgConn().ExecPrepared(ctx, "", nil, nil, nil).Read()
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			if _, err = h.backend.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN b int DEFAULT 2"); err != nil {
				t.Fatal(err)
			}
			desc, err := c.PgConn().Prepare(ctx, "", sql, nil)
			if err != nil {
				t.Fatalf("fresh unnamed Parse after DDL: %v", err)
			}
			after := c.PgConn().ExecPrepared(ctx, "", nil, nil, nil).Read()
			t.Logf("fresh Parse fields=%d; execution error=%v", len(desc.Fields), after.Err)
			if after.Err != nil || len(desc.Fields) != 2 || len(after.Rows) != 1 || len(after.Rows[0]) != 2 {
				t.Fatalf("new Parse retained stale result shape: %+v", after)
			}
		})
	}
}

func TestPooledExecModeRefreshesAfterDDL(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			id := uuid.NewString()
			role, pw := h.addRole(t, id)
			table := "ddl_exec_" + uuid.NewString()[:8]
			if _, err := h.backend.Exec(ctx, "CREATE TABLE "+table+" (a int); INSERT INTO "+table+" VALUES (1); GRANT SELECT ON "+table+" TO "+role); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
			host := id
			if pooled {
				host += pooledSuffix
			}
			host += "." + testSuffix
			c, err := h.connect(ctx, host, role, pw, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close(context.Background()) }()
			sql := "SELECT * FROM " + table
			columns := func() (int, error) {
				rows, err := c.Query(ctx, sql, pgx.QueryExecModeExec)
				if err != nil {
					return 0, err
				}
				defer rows.Close()
				n := 0
				for rows.Next() {
					values, err := rows.Values()
					if err != nil {
						return 0, err
					}
					n = len(values)
				}
				return n, rows.Err()
			}
			if n, err := columns(); err != nil || n != 1 {
				t.Fatalf("before DDL: n=%d err=%v", n, err)
			}
			if _, err = h.backend.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN b int DEFAULT 2"); err != nil {
				t.Fatal(err)
			}
			for attempt := 1; attempt <= 2; attempt++ {
				n, err := columns()
				t.Logf("fresh extended query attempt=%d columns=%d err=%v", attempt, n, err)
				if err != nil || n != 2 {
					t.Errorf("ordinary Exec query failed after migration: n=%d err=%v", n, err)
				}
			}
			if err := c.Close(ctx); err != nil {
				t.Fatal(err)
			}
			c, err = h.connect(ctx, host, role, pw, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := columns(); err != nil || n != 2 {
				t.Fatalf("a new client reused the old result shape: n=%d err=%v", n, err)
			}
		})
	}
}
