//go:build !no_pg

// adr: 1027
package pgproxy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPooledPreparedStatementsStayWithTheirClient(t *testing.T) {
	for _, tc := range []struct {
		label, name string
		pooled      bool
	}{
		{"pooled-named", "named", true},
		{"direct-unnamed", "", false},
		{"pooled-unnamed", "", true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			name := tc.name
			h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			databaseID := uuid.NewString()
			role, password := h.addRole(t, databaseID)
			host := databaseID
			if tc.pooled {
				host += pooledSuffix
			}
			host += "." + testSuffix
			a, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close(context.Background()) }()
			b, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.Close(context.Background()) }()
			if _, err := a.PgConn().Prepare(ctx, name, "SELECT 11::int", nil); err != nil {
				t.Fatal(err)
			}
			if _, err := b.PgConn().Prepare(ctx, name, "SELECT 22::int", nil); err != nil {
				t.Fatal(err)
			}
			result := a.PgConn().ExecPrepared(ctx, name, nil, nil, nil).Read()
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
				t.Fatalf("unexpected rows: %#v", result.Rows)
			}
			got := string(result.Rows[0][0])
			t.Logf("client A prepared SELECT 11; client B prepared SELECT 22; A received %s", got)
			if got != "11" {
				t.Fatalf("client A executed client B's SQL: got %q, want 11", got)
			}
		})
	}
}

func TestPooledUnnamedStatementSurvivesServerChange(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 2 })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id := uuid.NewString()
	role, password := h.addRole(t, id)
	host := id + pooledSuffix + "." + testSuffix
	a, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(context.Background()) }()
	b, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close(context.Background()) }()
	if _, err := a.PgConn().Prepare(ctx, "", "SELECT 11::int", nil); err != nil {
		t.Fatal(err)
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result := a.PgConn().ExecPrepared(ctx, "", nil, nil, nil).Read()
	if result.Err != nil || len(result.Rows) != 1 || string(result.Rows[0][0]) != "11" {
		t.Fatalf("unnamed statement on a different server: %+v", result)
	}
}

func TestPooledUnnamedStatementInvalidation(t *testing.T) {
	for _, action := range []string{"simple-query", "failed-simple-query", "close", "failed-parse"} {
		t.Run(action, func(t *testing.T) {
			h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			id := uuid.NewString()
			role, password := h.addRole(t, id)
			a, err := h.connect(ctx, id+pooledSuffix+"."+testSuffix, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close(context.Background()) }()
			if _, err := a.PgConn().Prepare(ctx, "", "SELECT 11::int", nil); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "simple-query":
				if _, err := a.PgConn().Exec(ctx, "SELECT 1").ReadAll(); err != nil {
					t.Fatal(err)
				}
			case "failed-simple-query":
				if _, err := a.PgConn().Exec(ctx, "SELECT 1/0").ReadAll(); sqlState(err) != "22012" {
					t.Fatalf("simple error: %v", err)
				}
			case "close":
				if err := a.PgConn().Deallocate(ctx, ""); err != nil {
					t.Fatal(err)
				}
			case "failed-parse":
				if _, err := a.PgConn().Prepare(ctx, "", "SELECT FROM", nil); sqlState(err) != "42601" {
					t.Fatalf("parse error: %v", err)
				}
			}
			result := a.PgConn().ExecPrepared(ctx, "", nil, nil, nil).Read()
			if sqlState(result.Err) != "26000" {
				t.Fatalf("invalidated unnamed statement executed: %v", result.Err)
			}
		})
	}
}

func TestPooledDescribeExecUsesItsOwnSQL(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	id := uuid.NewString()
	role, password := h.addRole(t, id)
	a, err := h.connect(ctx, id+pooledSuffix+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(context.Background()) }()
	for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeDescribeExec, pgx.QueryExecModeExec, pgx.QueryExecModeCacheDescribe} {
		var got int
		if err := a.QueryRow(ctx, "SELECT $1::int", mode, 11).Scan(&got); err != nil || got != 11 {
			t.Fatalf("mode %v: got=%d err=%v", mode, got, err)
		}
	}
}
