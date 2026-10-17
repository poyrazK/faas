package liveserver

import (
	"runtime/metrics"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/livequery"
)

// Metrics are the live-query metrics named in ADR-1384 section 7. A nil
// *Metrics records nothing.
type Metrics struct {
	subscriptions  prometheus.Gauge
	queries        prometheus.Gauge
	round          prometheus.Histogram
	commitToDelta  prometheus.Histogram
	recompute      prometheus.Counter
	resnapshots    *prometheus.CounterVec
	memory         prometheus.Gauge
	slotLag        prometheus.Gauge
	checkpoint     prometheus.Histogram
	slowConsumers  prometheus.Counter
	windowRefills  prometheus.Counter
	appliedTxns    prometheus.Counter
	maintenanceSec prometheus.Counter
	keys           prometheus.Gauge
	keyRefreshes   *prometheus.CounterVec
}

// NewMetrics registers the metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	latency := []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}
	m := &Metrics{
		subscriptions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "livequery_subscriptions", Help: "Live query subscriptions."}),
		queries:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "livequery_compiled_queries", Help: "Distinct compiled live queries."}),
		round: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "livequery_round_duration_seconds",
			Help: "Duration of one maintenance round.", Buckets: latency}),
		commitToDelta: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "livequery_commit_to_delta_seconds",
			Help: "Time from a transaction's commit to its round's frames being queued.", Buckets: latency}),
		recompute: prometheus.NewCounter(prometheus.CounterOpts{Name: "livequery_recompute_total",
			Help: "Full re-evaluations of a plan group (recompute mode, truncation, schema changes)."}),
		resnapshots: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "livequery_resnapshots_total",
			Help: "Queries read again from scratch, by reason."}, []string{"reason"}),
		memory:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "livequery_engine_memory_bytes", Help: "Live heap of the engine process after the last collection."}),
		slotLag: prometheus.NewGauge(prometheus.GaugeOpts{Name: "livequery_slot_lag_bytes", Help: "WAL the slot retains."}),
		checkpoint: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "livequery_checkpoint_duration_seconds",
			Help: "Duration of writing a checkpoint.", Buckets: latency}),
		slowConsumers: prometheus.NewCounter(prometheus.CounterOpts{Name: "livequery_slow_consumers_total",
			Help: "Connections closed because they fell too far behind."}),
		windowRefills: prometheus.NewCounter(prometheus.CounterOpts{Name: "livequery_window_refills_total",
			Help: "Ordered windows read again after losing rows."}),
		appliedTxns: prometheus.NewCounter(prometheus.CounterOpts{Name: "livequery_applied_transactions_total",
			Help: "Transactions applied to live queries."}),
		maintenanceSec: prometheus.NewCounter(prometheus.CounterOpts{Name: "livequery_maintenance_seconds_total",
			Help: "Time spent in maintenance statements."}),
		keys: prometheus.NewGauge(prometheus.GaugeOpts{Name: "livequery_keys", Help: "Live secrets the hub opens seals with."}),
		keyRefreshes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "livequery_key_refreshes_total",
			Help: "Reads of the database's live keys from the control plane, by result."}, []string{"result"}),
	}
	reg.MustRegister(m.subscriptions, m.queries, m.round, m.commitToDelta, m.recompute, m.resnapshots, m.memory,
		m.slotLag, m.checkpoint, m.slowConsumers, m.windowRefills, m.appliedTxns, m.maintenanceSec, m.keys, m.keyRefreshes)
	return m
}

func (m *Metrics) observeRound(d time.Duration, before, after livequery.Stats, subs, queries int) {
	if m == nil {
		return
	}
	m.round.Observe(d.Seconds())
	m.subscriptions.Set(float64(subs))
	m.queries.Set(float64(queries))
	m.recompute.Add(float64(after.Rebuilds - before.Rebuilds))
	m.resnapshots.WithLabelValues("schema_changed").Add(float64(after.Recompiles - before.Recompiles))
	m.windowRefills.Add(float64(after.WindowRefills - before.WindowRefills))
	m.appliedTxns.Add(float64(after.Applied - before.Applied))
	m.maintenanceSec.Add((after.StatementTime - before.StatementTime).Seconds())
	// The heap that survived the last collection: what the engine keeps,
	// not the garbage a round leaves behind.
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() == metrics.KindUint64 {
		m.memory.Set(float64(sample[0].Value.Uint64()))
	}
}

func (m *Metrics) observeCommitToDelta(d time.Duration) {
	if m != nil {
		m.commitToDelta.Observe(d.Seconds())
	}
}

func (m *Metrics) observeCheckpoint(d time.Duration) {
	if m != nil {
		m.checkpoint.Observe(d.Seconds())
	}
}

func (m *Metrics) slowConsumer() {
	if m != nil {
		m.slowConsumers.Inc()
	}
}

// SlotLost counts a restore that found its slot gone: every query starts
// over.
func (m *Metrics) SlotLost(queries int) {
	if m != nil {
		m.resnapshots.WithLabelValues("slot_lost").Add(float64(queries))
	}
}

func (m *Metrics) keyRefresh(result string) {
	if m != nil {
		m.keyRefreshes.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) setKeys(n int) {
	if m != nil {
		m.keys.Set(float64(n))
	}
}

// SetSlotLag records the WAL the slot retains.
func (m *Metrics) SetSlotLag(bytes int64) {
	if m != nil {
		m.slotLag.Set(float64(bytes))
	}
}
