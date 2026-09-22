// pool_metrics.go — per-daemon Postgres connection-pool observability.
//
// The fleet's Postgres capacity is derived arithmetically:
// postgres_capacity computes max_connections from DaemonMaxConnections summed
// over the control plane and every compute node, so the per-daemon caps in
// db.go are what decide how many nodes a database host can carry. Those caps
// have never been measurable. Nothing in the tree exported a single pool
// statistic, so every question an operator actually has —
//
//   - is this daemon near its cap, or nowhere close?
//   - did a request wait for a connection, or get one immediately?
//   - did the ADR-190 notify hub really collapse N parked LISTEN sessions
//     into one, or is a subscriber still holding its own?
//
// — could only be answered by reading pg_stat_activity on a live box, which
// is exactly what happened during the 2026-09-12 fsn-1 exhaustion (96/97
// slots used, 88 idle, every daemon dying on SQLSTATE 53300).
//
// This collector closes that gap. It is a prometheus.Collector rather than a
// polling goroutine: pgxpool.Pool.Stat() is a cheap snapshot of counters the
// pool already maintains, so sampling it at scrape time costs nothing between
// scrapes and can never report a value staler than the scrape itself.
//
// The hub gauges sit here rather than beside the ADR-190 counters on purpose.
// db_notify_hub_reconnects_total and _dropped_total report hub *health*; what
// sizing needs is hub *shape* — how many subscribers are riding one
// connection. Reading that next to db_pool_conns is what makes a budget cut
// an evidence-backed change rather than an arithmetic guess.
package db

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector reports one pool's live statistics plus the shape of the
// notify hub riding on it. Safe for a nil pool: Collect emits nothing, which
// keeps a daemon that has not opened Postgres yet from failing its scrape.
type PoolCollector struct {
	pool *pgxpool.Pool

	conns          *prometheus.Desc
	maxConns       *prometheus.Desc
	acquires       *prometheus.Desc
	emptyAcquires  *prometheus.Desc
	canceled       *prometheus.Desc
	acquireWait    *prometheus.Desc
	hubConns       *prometheus.Desc
	hubSubscribers *prometheus.Desc
	hubChannels    *prometheus.Desc
}

// NewPoolCollector builds the collector for one daemon's pool. prefix is the
// daemon's metric prefix (the same one OpsMetrics uses), so the series sort
// next to the rest of that daemon's metrics.
func NewPoolCollector(prefix string, pool *pgxpool.Pool) *PoolCollector {
	return &PoolCollector{
		pool: pool,
		conns: prometheus.NewDesc(
			prefix+"_db_pool_conns",
			"Current Postgres pool connections held by this daemon, labelled by state ∈ {acquired, idle, constructing}. `acquired` is in-flight work; `idle` is warm capacity the pool is holding open; `constructing` is a dial in progress. Compare the sum against <prefix>_db_pool_max_conns to see how close the daemon runs to the cap in pkg/db.DaemonMaxConnections, which is the per-daemon input the postgres_capacity Ansible role multiplies across the fleet to derive max_connections.",
			[]string{"state"}, nil,
		),
		maxConns: prometheus.NewDesc(
			prefix+"_db_pool_max_conns",
			"This daemon's configured pool ceiling (pkg/db.DaemonMaxConnections). Exported so a dashboard can chart utilisation as a ratio without hard-coding the cap, and so a cap change is visible in the metric stream rather than only in a deploy diff.",
			nil, nil,
		),
		acquires: prometheus.NewDesc(
			prefix+"_db_pool_acquires_total",
			"Cumulative successful pool acquisitions. The denominator for the empty-acquire ratio below.",
			nil, nil,
		),
		emptyAcquires: prometheus.NewDesc(
			prefix+"_db_pool_empty_acquires_total",
			"Cumulative acquisitions that found no idle connection and therefore had to construct one or wait for a release. NOT a pure saturation signal: pools are configured MinConns=0, so the first acquisitions after boot necessarily count here while the pool warms up. Read it warm and as a rate — a sustained rate once <prefix>_db_pool_conns has plateaued means callers routinely find the pool empty. For unambiguous saturation use canceled_acquires_total and acquire_wait_seconds_total below.",
			nil, nil,
		),
		canceled: prometheus.NewDesc(
			prefix+"_db_pool_canceled_acquires_total",
			"Cumulative acquisitions abandoned because the caller's context ended while waiting for a connection. This is the unambiguous saturation signal — it cannot fire during warm-up, only when a caller gave up waiting — and any non-zero rate is pool starvation already surfacing as customer-visible timeouts. It must be zero before any entry in DaemonMaxConnections is lowered.",
			nil, nil,
		),
		acquireWait: prometheus.NewDesc(
			prefix+"_db_pool_acquire_wait_seconds_total",
			"Cumulative seconds spent blocked waiting for a pool connection. Near-zero growth on a busy daemon is the positive evidence that a cap has headroom to give back; divided by acquires_total it gives the mean wait per acquisition, which separates 'rarely waits but waits a long time' from 'waits constantly but briefly'.",
			nil, nil,
		),
		hubConns: prometheus.NewDesc(
			prefix+"_db_notify_hub_conns",
			"Connections this daemon's pool has parked on the ADR-190 LISTEN hub: 1 while the hub is running, 0 otherwise. Before the hub, every SubscribeWithReconnect call parked its own connection; this gauge read against _db_notify_hub_subscribers is the direct evidence of how many sessions the hub is saving. A 0 here with a non-zero subscriber count means FAAS_DB_NOTIFY_HUB=0 is set and the daemon is back to one connection per subscriber.",
			nil, nil,
		),
		hubSubscribers: prometheus.NewDesc(
			prefix+"_db_notify_hub_subscribers",
			"Live subscribers multiplexed onto this daemon's hub connection. Under the pre-ADR-190 model this number equalled parked connections; the gap between it and _db_notify_hub_conns is the pool headroom the hub returned, and therefore the headroom available to a DaemonMaxConnections reduction.",
			nil, nil,
		),
		hubChannels: prometheus.NewDesc(
			prefix+"_db_notify_hub_channels",
			"Distinct Postgres channels this daemon's hub is LISTENing on. Lower than the subscriber count whenever several subscribers share a channel; a channel count that keeps climbing is the tripwire for a per-entity channel name leaking into what should be a fixed vocabulary.",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	if c == nil {
		return
	}
	for _, d := range []*prometheus.Desc{
		c.conns, c.maxConns, c.acquires, c.emptyAcquires,
		c.canceled, c.acquireWait, c.hubConns, c.hubSubscribers, c.hubChannels,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector. A nil collector or nil pool emits
// nothing rather than failing the scrape: a daemon whose Postgres handle is
// constructed lazily must still serve /metrics before that happens.
func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.pool == nil {
		return
	}
	stat := c.pool.Stat()
	if stat == nil {
		return
	}
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}

	gauge(c.conns, float64(stat.AcquiredConns()), "acquired")
	gauge(c.conns, float64(stat.IdleConns()), "idle")
	gauge(c.conns, float64(stat.ConstructingConns()), "constructing")
	gauge(c.maxConns, float64(stat.MaxConns()))

	counter(c.acquires, float64(stat.AcquireCount()))
	counter(c.emptyAcquires, float64(stat.EmptyAcquireCount()))
	counter(c.canceled, float64(stat.CanceledAcquireCount()))
	counter(c.acquireWait, stat.AcquireDuration().Seconds())

	hub := NotifyHubStatsFor(c.pool)
	var hubConns float64
	if hub.Running {
		hubConns = 1
	}
	gauge(c.hubConns, hubConns)
	gauge(c.hubSubscribers, float64(hub.Subscribers))
	gauge(c.hubChannels, float64(hub.Channels))
}
