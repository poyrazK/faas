// pool_metrics.go — wiring for the per-daemon Postgres pool collector.
//
// The collector itself lives in pkg/db (it reads pgxpool and notify-hub
// internals). This file is the one-line call site every daemon uses, so the
// metric prefix always comes from the same OpsMetrics that owns the registry
// rather than from a string repeated at eleven call sites — a prefix typo
// would otherwise put a daemon's pool series under a name no dashboard
// queries, which is the failure mode the SetOpsMetrics pattern exists to
// prevent.
package wire

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// RegisterPoolMetrics attaches the daemon's Postgres pool statistics to its
// /metrics registry. Call it once, after the pool is open, in the same run()
// that constructed ops.
//
// Nil ops or nil pool is a no-op: daemons that open Postgres lazily (schedd,
// vmmd, imaged, builderd, meterd all construct through a dialer closure)
// would otherwise have to guard the call themselves, and a daemon that never
// reaches Postgres must still serve /metrics.
//
// Registration errors are swallowed deliberately. The only realistic error is
// AlreadyRegisteredError from a double call in a test harness; failing a
// daemon's boot over a duplicate observability collector would trade a real
// outage for a metric.
func RegisterPoolMetrics(ops *OpsMetrics, pool *pgxpool.Pool) {
	if ops == nil || pool == nil {
		return
	}
	reg := ops.Registry()
	if reg == nil {
		return
	}
	_ = reg.Register(db.NewPoolCollector(ops.MetricPrefix(), pool))
}
