// direct.go — the session-scoped ("direct") pool alongside the ordinary one.
//
// Why two pools. The fleet's Postgres capacity is derived from
// DaemonMaxConnections summed over the control plane and every compute node,
// so connections scale linearly with fleet size: the postgres_capacity role
// derives 610 connections at 12 nodes and 4,600 at 100. PostgreSQL is not
// operable at 4,600 backends, and the answer everyone reaches for —
// transaction-mode PgBouncer — cannot carry this codebase as it stands,
// because transaction pooling returns the server connection to the pool at
// the end of every transaction and therefore destroys anything session-scoped.
//
// This repo has exactly two kinds of session-scoped work, and both are
// load-bearing:
//
//   - LISTEN. The ADR-190 notify hub, the legacy Subscribe path, and
//     WaitFor all hold a connection open to receive pg_notify. A pooled
//     connection would be handed to another client between notifications.
//   - Session advisory locks. PgStore's edge-rule mutation lock and
//     MigrateUp's migration lock both take pg_advisory_lock (NOT the
//     _xact_ variant) on a pinned connection and hold it across statements.
//     Under transaction pooling the lock would be released — or worse,
//     leak to whichever client got that server connection next.
//
// Everything else in the tree is transaction-scoped and pools safely.
// pg_advisory_xact_lock (ADR-193's node reservation, the workflow lock, the
// managed-secret target lock) releases at COMMIT by definition, and there are
// no temp tables, no cursors held outside a transaction, and no session-level
// SET in the Go code.
//
// So the split is: ordinary queries may go through a pooler; the sites listed
// above resolve to a direct connection to Postgres instead. The seam is a
// pool-to-pool registry rather than a signature change on db.Open, because
// every session-scoped call site already receives the daemon's pool as an
// argument — threading a second pool through eleven daemons would touch far
// more code than the behaviour warrants, and a call site that FORGOT to
// thread it would silently do session work on a pooled connection, which is
// the failure this file exists to prevent.
//
// Until FAAS_DATABASE_URL_DIRECT is set, the direct pool IS the ordinary
// pool, so this file changes nothing about how the platform runs today.
package db

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DirectDSNEnv points session-scoped work at Postgres directly, bypassing a
// pooler that the ordinary DSN goes through. Unset means "no pooler in play":
// the direct pool is the ordinary pool and nothing changes.
//
// The value is a full DSN rather than a flag because the two paths differ in
// host and port, not merely in routing — the pooled DSN points at PgBouncer,
// this one at the postmaster.
const DirectDSNEnv = "FAAS_DATABASE_URL_DIRECT"

// directMaxConns caps the session-scoped pool. It is deliberately small: with
// the ADR-190 hub a daemon parks exactly one LISTEN connection, and the two
// advisory-lock sites are a per-app edge-rule mutation and a once-per-boot
// migration. Four leaves room for both plus a spare without re-inflating the
// per-daemon budget the split exists to shrink.
const directMaxConns int32 = 4

var (
	directMu    sync.RWMutex
	directPools = map[*pgxpool.Pool]*pgxpool.Pool{}
)

// registerDirectPool records the session-scoped sibling for an ordinary pool.
// A nil or identical direct pool is recorded as "no sibling", which makes
// DirectPool return the ordinary pool unchanged.
func registerDirectPool(ordinary, direct *pgxpool.Pool) {
	if ordinary == nil || direct == nil || ordinary == direct {
		return
	}
	directMu.Lock()
	directPools[ordinary] = direct
	directMu.Unlock()
}

// DirectPool returns the pool that session-scoped work must use for p.
//
// When no direct sibling is registered — the default, and every deployment
// without a pooler — it returns p itself, so callers are correct either way
// and need no branch of their own.
func DirectPool(p *pgxpool.Pool) *pgxpool.Pool {
	if p == nil {
		return nil
	}
	directMu.RLock()
	direct, ok := directPools[p]
	directMu.RUnlock()
	if ok && direct != nil {
		return direct
	}
	return p
}

// closeDirectPool closes and unregisters the sibling of p, if any. Called by
// the ordinary pool's owner so a daemon that closes its pool does not leak
// the direct one.
func closeDirectPool(p *pgxpool.Pool) {
	if p == nil {
		return
	}
	directMu.Lock()
	direct, ok := directPools[p]
	delete(directPools, p)
	directMu.Unlock()
	if ok && direct != nil {
		direct.Close()
	}
}

// openDirect builds the session-scoped pool when DirectDSNEnv names one.
// Returns nil when unset, which registerDirectPool treats as "no sibling".
func openDirect(ctx context.Context, appName string) (*pgxpool.Pool, error) {
	dsn := os.Getenv(DirectDSNEnv)
	if dsn == "" {
		return nil, nil
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse %s: %w", DirectDSNEnv, err)
	}
	cfg.MaxConns = directMaxConns
	cfg.MinConns = 0
	// No MaxConnIdleTime: a LISTEN connection is idle by definition between
	// notifications, and reaping it would make the hub reconnect on a timer.
	cfg.HealthCheckPeriod = healthCheckPeriod
	if appName != "" {
		// The direct path reaches the postmaster, so application_name still
		// identifies the daemon in pg_stat_activity — which is where an
		// operator looks when a LISTEN or an advisory lock misbehaves.
		cfg.ConnConfig.RuntimeParams["application_name"] = appName + "-direct"
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open direct pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping direct pool: %w", err)
	}
	return pool, nil
}

// applyPooledExecMode makes the ordinary pool safe to run through a
// transaction-mode pooler.
//
// pgx defaults to QueryExecModeCacheStatement, which issues named PREPARE
// statements and reuses them by name. Under transaction pooling the next
// transaction may land on a different server connection where that name was
// never prepared, and the query fails with "prepared statement does not
// exist". PgBouncer 1.21+ can track prepared statements itself, but relying
// on that couples correctness to the pooler's version and configuration;
// QueryExecModeExec sends the query and its parameters together every time,
// which is correct against any pooler and against no pooler at all.
//
// Applied ONLY when a direct DSN is configured. Without a pooler the default
// mode is faster and there is nothing to be safe from, so a deployment that
// has not opted in keeps today's behaviour exactly.
func applyPooledExecMode(cfg *pgxpool.Config) {
	if os.Getenv(DirectDSNEnv) == "" {
		return
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
}
