// Package db — Postgres connection + migrations + LISTEN/NOTIFY helpers.
//
// Spec §4.2 / §5: apid, schedd, imaged, builderd (M6) all share the same
// Postgres cluster. This package owns the connection lifecycle and the
// notification channels the daemons use to coordinate without direct calls
// (CLAUDE.md §Component ownership: "components talk via Postgres rows +
// pg_notify, or gRPC on unix sockets").
//
// Migrations are baked into the binary via embed.FS and applied on startup
// with goose; the schema is the source of truth in migrations/*.sql.
package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// Open dials Postgres and returns a connection pool. DSN precedence:
//  1. $DATABASE_URL
//  2. $FAAS_DATABASE_URL
//  3. default `postgres:///faas?host=/run/postgresql&user=faas` (peer auth,
//     matches the ansible postgres role).
func Open(ctx context.Context, dsnOverride string) (*pgxpool.Pool, error) {
	return open(ctx, dsnOverride, "")
}

// DaemonMaxConnections is the direct-pool budget for each production daemon.
// The normal public-beta topology (control plane plus one active compute node)
// stays below 90 ordinary sessions. A second continuously active compute node
// requires the PgBouncer connection-capacity gate documented in the Postgres
// runbook.
var DaemonMaxConnections = map[string]int32{
	"apid":   12,
	// Each schedd owns eleven permanent LISTEN subscribers in production.
	// Keep five slots for readiness probes, scheduler queries, and dispatch
	// transactions. A cap of twelve leaves only one request slot and makes
	// /readyz fail as soon as one background query holds it.
	"schedd": 16,
	// gatewayd-internal owns six permanent LISTEN subscribers in the
	// production configuration. Reserve two further slots for request-path
	// reads and startup reconciliation; a cap of three deadlocks before
	// sd_notify(READY=1) because the first three subscribers exhaust the pool.
	"gatewayd-internal": 8,
	"gatewayd-public":   3,
	"vmmd":              4,
	"imaged":            3,
	"builderd":          3,
	"meterd":            3,
	"githubd":           2,
	"outboundd":         2,
	"s3-gatewayd":       2,
}

const defaultMaxConnections int32 = 4

// OpenWithAppName is Open plus an application_name tag set on every
// connection pgxpool acquires. The tag is sent at session-start (via
// RuntimeParams), so it survives on the long-lived LISTEN connection
// that schedd/builderd/imaged hold for pg_notify — the e2e harness
// races on this name in pg_stat_activity rather than on `query ILIKE
// '%LISTEN%…%'`, which can match the wrong session across rapid
// restart cycles.
func OpenWithAppName(ctx context.Context, dsnOverride, appName string) (*pgxpool.Pool, error) {
	return open(ctx, dsnOverride, appName)
}

func daemonMaxConnections(appName string) int32 {
	name := strings.TrimPrefix(strings.TrimSpace(appName), "faas-")
	if limit, ok := DaemonMaxConnections[name]; ok {
		return limit
	}
	return defaultMaxConnections
}

func open(ctx context.Context, dsnOverride, appName string) (*pgxpool.Pool, error) {
	dsn := dsnOverride
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("FAAS_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres:///faas?host=/run/postgresql&user=faas"
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	// Direct pools have per-daemon budgets. MinConns=0 and the short idle
	// lifetime keep burst capacity from becoming permanent idle ClientRead
	// sessions; LISTEN subscribers naturally retain only the sessions they use.
	cfg.MaxConns = daemonMaxConnections(appName)
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	if appName != "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = appName
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// WithBudget wraps a per-DB-call context with a reqbudget.WithOverhead
// reservation against the inbound request's end-to-end budget. When
// the inbound ctx carries a Budget, the next DB hop starts with
// less declared budget than its parent had. When no Budget is
// attached (a CLI / background path), the helper is the identity
// no-op.
//
// ADR-093 / PR-E: the canonical place to wrap each SQL call. Callers
// can either use it inline (preferred for hot paths) or trust the
// ctx that's already on r.Context() (the inbound budget already
// propagates via pgxpool — the overhead reservation is the
// bookkeeping for the audit trail + per-hop metric label, not a
// wall-clock extension). Production daemons (apid, schedd, vmmd,
// imaged) are expected to call this once per hot-path Store method.
//
// The cost is reqbudget.DefaultOverheadDB (10 ms — a local PG
// round-trip reservation). It is a DECLARED budget reduction, not
// measured — the actual DB round-trip is whatever it is.
func WithBudget(parent context.Context) context.Context {
	b, ok := reqbudget.FromContext(parent)
	if !ok {
		return parent
	}
	newCtx, _, _ := b.WithOverhead(parent, "db", reqbudget.DefaultOverheadDB)
	return newCtx
}
