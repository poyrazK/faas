//go:build !no_pg

// adr: 1027
package pgproxy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFailedBatchDoesNotReplaceNamedStatement(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		label := "direct"
		if pooled {
			label = "pooled"
		}
		t.Run(label, func(t *testing.T) {
			h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			id := uuid.NewString()
			role, password := h.addRole(t, id)
			host := id
			if pooled {
				host += pooledSuffix
			}
			host += "." + testSuffix
			conn, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			if _, err := conn.PgConn().Prepare(ctx, "kept", "SELECT 11::int", nil); err != nil {
				t.Fatal(err)
			}
			pipeline := conn.PgConn().StartPipeline(ctx)
			pipeline.SendQueryParams("SELECT 1/0", nil, nil, nil, nil)
			// Replace the statement legally: Close, then Parse. PostgreSQL skips
			// both after the division-by-zero error.
			pipeline.SendDeallocate("kept")
			pipeline.SendPrepare("kept", "SELECT 22::int", nil)
			if err := pipeline.Sync(); err != nil {
				t.Fatal(err)
			}
			var sawDivision bool
			for {
				result, err := pipeline.GetResults()
				if sqlState(err) == "22012" {
					sawDivision = true
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if result == nil {
					break
				}
				if rr, ok := result.(*pgconn.ResultReader); ok {
					if _, err := rr.Close(); sqlState(err) == "22012" {
						sawDivision = true
					} else if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := pipeline.Close(); err != nil {
				t.Fatal(err)
			}
			if !sawDivision {
				t.Fatal("pipeline did not fail as expected")
			}
			result := conn.PgConn().ExecPrepared(ctx, "kept", nil, nil, nil).Read()
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
				t.Fatalf("rows: %#v", result.Rows)
			}
			got := string(result.Rows[0][0])
			t.Logf("kept prepared SELECT 11, skipped replacement SELECT 22, execution returned %s", got)
			if got != "11" {
				t.Fatal("a skipped Parse changed the client's named statement")
			}
		})
	}
}

func TestSkippedNamedCloseKeepsTheStatement(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id := uuid.NewString()
	role, password := h.addRole(t, id)
	conn, err := h.connect(ctx, id+pooledSuffix+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.PgConn().Prepare(ctx, "kept", "SELECT 11::int", nil); err != nil {
		t.Fatal(err)
	}
	pipe := conn.PgConn().StartPipeline(ctx)
	pipe.SendQueryParams("SELECT 1/0", nil, nil, nil, nil)
	pipe.SendDeallocate("kept")
	if err := pipe.Sync(); err != nil {
		t.Fatal(err)
	}
	for {
		result, err := pipe.GetResults()
		if sqlState(err) == "22012" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if result == nil {
			break
		}
		if rr, ok := result.(*pgconn.ResultReader); ok {
			_, _ = rr.Close()
		}
	}
	if err := pipe.Close(); err != nil {
		t.Fatal(err)
	}
	result := conn.PgConn().ExecPrepared(ctx, "kept", nil, nil, nil).Read()
	if result.Err != nil || len(result.Rows) != 1 || string(result.Rows[0][0]) != "11" {
		t.Fatalf("skipped Close removed the statement: %+v", result)
	}
}
