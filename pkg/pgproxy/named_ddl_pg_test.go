//go:build !no_pg

package pgproxy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func namedDDLFixture(t *testing.T, pooled bool) (*harness, string, string, string, string) {
	t.Helper()
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	id := uuid.NewString()
	role, pw := h.addRole(t, id)
	table := "audit_ddl_" + uuid.NewString()[:8]
	if _, err := h.backend.Exec(t.Context(), "CREATE TABLE "+table+" (a int); INSERT INTO "+table+" VALUES (1); GRANT SELECT ON "+table+" TO "+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	host := id
	if pooled {
		host += pooledSuffix
	}
	return h, host + "." + testSuffix, role, pw, table
}

func cachedQueryColumns(ctx context.Context, c *pgx.Conn, query string) (int, error) {
	rows, err := c.Query(ctx, query)
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

func TestPooledNamedParseRefreshesAfterDDL(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		for _, closeName := range []bool{false, true} {
			t.Run(fmt.Sprintf("pooled=%v/close=%v", pooled, closeName), func(t *testing.T) {
				h, host, role, pw, table := namedDDLFixture(t, pooled)
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				c, err := h.connect(ctx, host, role, pw, h.dbName, "require", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = c.Close(context.Background()) }()
				query := "SELECT * FROM " + table
				if _, err = c.PgConn().Prepare(ctx, "before", query, nil); err != nil {
					t.Fatal(err)
				}
				if result := c.PgConn().ExecPrepared(ctx, "before", nil, nil, nil).Read(); result.Err != nil {
					t.Fatal(result.Err)
				}
				name := "after"
				if closeName {
					if err = c.PgConn().Deallocate(ctx, "before"); err != nil {
						t.Fatal(err)
					}
					name = "before"
				}
				if _, err = h.backend.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN b int DEFAULT 2"); err != nil {
					t.Fatal(err)
				}
				desc, err := c.PgConn().Prepare(ctx, name, query, nil)
				if err != nil {
					t.Fatalf("fresh named Parse failed after DDL: %v", err)
				}
				result := c.PgConn().ExecPrepared(ctx, name, nil, nil, nil).Read()
				if result.Err != nil || len(desc.Fields) != 2 || len(result.Rows) != 1 || len(result.Rows[0]) != 2 {
					t.Fatalf("fresh named statement retained stale row shape: fields=%d result=%+v", len(desc.Fields), result)
				}
			})
		}
	}
}

func TestPooledCachedQueryReconnectsAfterDDL(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			h, host, role, pw, table := namedDDLFixture(t, pooled)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			c, err := h.connect(ctx, host, role, pw, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			query := "SELECT * FROM " + table
			if n, err := cachedQueryColumns(ctx, c, query); err != nil || n != 1 {
				t.Fatalf("initial query: columns=%d err=%v", n, err)
			}
			_ = c.Close(ctx)
			if _, err = h.backend.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN b int DEFAULT 2"); err != nil {
				t.Fatal(err)
			}
			for client := 1; client <= 2; client++ {
				c, err = h.connect(ctx, host, role, pw, h.dbName, "require", nil)
				if err != nil {
					t.Fatal(err)
				}
				for attempt := 1; attempt <= 2; attempt++ {
					n, err := cachedQueryColumns(ctx, c, query)
					t.Logf("client=%d attempt=%d columns=%d err=%v", client, attempt, n, err)
					if err != nil || n != 2 {
						t.Errorf("fresh client/default pgx query could not recover after DDL: columns=%d err=%v", n, err)
					}
				}
				_ = c.Close(ctx)
			}
		})
	}
}
