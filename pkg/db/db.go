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

// DaemonMaxConnections is the direct-pool budget for each production daemon
// when the ADR-190 notify hub is active — which is every production
// deployment, since the hub is on unless FAAS_DB_NOTIFY_HUB=0.
//
// The postgres_capacity Ansible role combines these per-process limits with
// the complete compute_nodes inventory. It keeps the steady fleet below 75%
// of ordinary PostgreSQL capacity and reserves one overlapping compute
// generation plus operator headroom before node admission. Because the role
// multiplies these numbers across the control plane and every compute node,
// a single entry here sets the RAM requirement of the database host for the
// whole fleet: a 12-node fleet already derives 610 connections.
//
// Sizing rule. With the hub on, a daemon parks exactly ONE connection for
// notifications no matter how many channels it subscribes to, so an entry
// here is (1 notify connection + concurrent query/transaction demand). The
// values below are still the pre-hub numbers, which reserved a slot per
// subscriber; lowering them needs production evidence from the pool metrics
// (pool_metrics.go) and is deliberately NOT part of introducing this split.
// The precondition is recorded in docs/runbooks/FaasDBPoolStarved.md: a
// flat-zero <daemon>_db_pool_canceled_acquires_total on a busy daemon.
var DaemonMaxConnections = map[string]int32{
	"apid":              12,
	"schedd":            16,
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

// DaemonMaxConnectionsNotifyHubDisabled is the budget used when
// FAAS_DB_NOTIFY_HUB=0 restores the pre-ADR-190 behaviour of one parked
// LISTEN connection per subscriber.
//
// This table exists so the kill switch stays a safe rollback. Without it,
// the two settings share one budget and the numbers can only ever be sized
// for the worse case — meaning the headroom the hub actually returned can
// never be reclaimed, because reclaiming it would turn
// FAAS_DB_NOTIFY_HUB=0 into a fleet-wide boot failure. Daemons subscribe
// from many places (apid, schedd and gatewayd-internal each from more than
// twenty call sites), so a pool sized for the hub cannot seat one connection
// per subscriber; the daemon would block inside its first subscriptions and
// never reach sd_notify(READY=1).
//
// The values are the pre-hub numbers, which is what the old shared table
// already held. They are the ceiling this file must keep honouring, not a
// budget anyone is expected to tune: the supported configuration is the hub.
//
// Entries absent here fall back to the hub-on table, then to
// defaultMaxConnections — a daemon with no explicit budget has no LISTEN
// subscribers worth reserving for either.
var DaemonMaxConnectionsNotifyHubDisabled = map[string]int32{
	// Each schedd owns eleven permanent LISTEN subscribers on this path.
	// Keep five slots for readiness probes, scheduler queries, and dispatch
	// transactions. A cap of twelve leaves only one request slot and makes
	// /readyz fail as soon as one background query holds it.
	"schedd": 16,
	// gatewayd-internal owns six permanent LISTEN subscribers on this path.
	// Reserve two further slots for request-path reads and startup
	// reconciliation; a cap of three deadlocks before sd_notify(READY=1)
	// because the first three subscribers exhaust the pool.
	"gatewayd-internal": 8,
	"apid":              12,
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

// Shared with direct.go so both pools age and probe identically.
const (
	healthCheckPeriod = 30 * time.Second
	pingTimeout       = 5 * time.Second
)

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

// daemonMaxConnections resolves a daemon's pool budget for the notify mode
// this process is running in.
//
// The mode is read here rather than passed in because the budget is fixed at
// pool construction while the hub decision is made per Subscribe call — both
// read the same env var, and reading it at the same layer keeps them from
// drifting apart. A daemon cannot change mode without a restart, and a
// restart re-opens the pool.
func daemonMaxConnections(appName string) int32 {
	name := strings.TrimPrefix(strings.TrimSpace(appName), "faas-")
	if !notifyHubEnabled() {
		if limit, ok := DaemonMaxConnectionsNotifyHubDisabled[name]; ok {
			return limit
		}
	}
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
	cfg.HealthCheckPeriod = healthCheckPeriod
	if appName != "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = appName
	}
	// Safe under a transaction-mode pooler; a no-op without one.
	applyPooledExecMode(cfg)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	// Session-scoped work (LISTEN, session advisory locks) resolves through
	// DirectPool to this sibling. Unset FAAS_DATABASE_URL_DIRECT leaves it
	// nil, and DirectPool then returns the ordinary pool — today's behaviour.
	direct, err := openDirect(ctx, appName)
	if err != nil {
		pool.Close()
		return nil, err
	}
	registerDirectPool(pool, direct)
	return pool, nil
}

// Close closes p and its session-scoped sibling. Daemons that call
// pool.Close() directly still work — they simply leave the direct pool to
// process exit, which is the same lifetime it had before this split.
func Close(p *pgxpool.Pool) {
	if p == nil {
		return
	}
	closeDirectPool(p)
	p.Close()
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
