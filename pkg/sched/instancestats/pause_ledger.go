package instancestats

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// PauseLedger records the quiet pauses each node reports in the quiet pause
// ledger, which meterd subtracts from billed residency (ADR-20261023171905).
//
// vmmd reports each instance's open pause and its most recent completed one.
// A completed pause is recorded exactly. An open pause is recorded once, and
// the node's watermark then says how far it is known to continue. When a
// pause ends without its completed report reaching the ledger (it was
// replaced before a report, the VM was destroyed while paused, or vmmd
// restarted), the ledger closes it at the last report that saw it paused, so
// a missed report is billed and never subtracted.
type PauseLedger struct {
	store      state.QuietPauseLedgerStore
	flushEvery time.Duration
	log        *slog.Logger
	metrics    *PauseLedgerMetrics

	mu    sync.Mutex
	nodes map[string]*ledgerNode
}

// ledgerNode is one node's ledger state between flushes.
type ledgerNode struct {
	// loaded is set once the node's open pauses were read back from the
	// store, so a restarted schedd can close the ones that ended meanwhile.
	loaded bool
	// open is each instance's open pause, by start, with the last report
	// that saw it paused.
	open map[string]openPause
	// closed is the start of each instance's completed pause last queued:
	// vmmd repeats it in every report until the next pause completes.
	closed map[string]time.Time
	// pending is written by the next flush, keyed by instance and start.
	pending map[pauseKey]time.Time
	// reported is the newest report processed; covered is the newest report
	// whose pauses are all pending or recorded.
	reported, covered time.Time
	lastFlush         time.Time
	// tallies is the paused time vmmd had measured for each instance's live
	// incarnation at the node's previous report (measure).
	tallies map[string]pausedTally
}

// pausedTally is the quiet-paused time vmmd had measured for one
// incarnation: its completed pauses, and how long its open pause had lasted.
type pausedTally struct {
	total, open time.Duration
}

// pauseLedgerCallTimeout bounds one ledger read or write. The ledger runs on
// the poller's tick, which admission's stats depend on, so a slow database
// delays the ledger, not the tick.
const pauseLedgerCallTimeout = statsCallTimeout

type openPause struct {
	start, lastSeen time.Time
}

type pauseKey struct {
	instance string
	start    time.Time
}

// NewPauseLedger returns a ledger that writes each node's report at most
// every flushEvery.
func NewPauseLedger(store state.QuietPauseLedgerStore, flushEvery time.Duration, log *slog.Logger, metrics *PauseLedgerMetrics) *PauseLedger {
	if log == nil {
		log = slog.Default()
	}
	return &PauseLedger{store: store, flushEvery: flushEvery, log: log, metrics: metrics, nodes: make(map[string]*ledgerNode)}
}

// Observe takes one poller tick's rows. A node's rows form one report, and a
// report already seen (the same SampledAt) is skipped: the poller projects
// the same node batch every tick until the next one arrives.
func (l *PauseLedger) Observe(ctx context.Context, rows []InstanceStat, now time.Time) {
	if l == nil {
		return
	}
	byNode := make(map[string][]InstanceStat)
	for _, row := range rows {
		if row.NodeID != "" && row.InstanceID != "" {
			byNode[row.NodeID] = append(byNode[row.NodeID], row)
		}
	}
	nodes := make([]string, 0, len(byNode))
	for nodeID := range byNode {
		nodes = append(nodes, nodeID)
	}
	sort.Strings(nodes)
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, nodeID := range nodes {
		report := byNode[nodeID]
		node := l.nodeLocked(ctx, nodeID)
		if node == nil {
			continue
		}
		sampled := report[0].SampledAt
		if sampled.IsZero() || !sampled.After(node.reported) {
			continue
		}
		node.reported = sampled
		node.apply(report, sampled)
		l.metrics.measured(nodeID, node.measure(report, sampled))
		node.covered = sampled
		l.flushLocked(ctx, nodeID, node, now)
	}
}

// nodeLocked returns nodeID's state, loading its open pauses on first use.
// It returns nil while the store cannot be read; the node's reports wait.
func (l *PauseLedger) nodeLocked(ctx context.Context, nodeID string) *ledgerNode {
	node := l.nodes[nodeID]
	if node == nil {
		node = &ledgerNode{open: make(map[string]openPause), closed: make(map[string]time.Time), pending: make(map[pauseKey]time.Time), tallies: make(map[string]pausedTally)}
		l.nodes[nodeID] = node
	}
	if node.loaded {
		return node
	}
	loadCtx, cancel := context.WithTimeout(ctx, pauseLedgerCallTimeout)
	open, err := l.store.OpenQuietPauses(loadCtx, nodeID)
	cancel()
	if err != nil {
		l.metrics.write("load_error")
		l.log.Warn("quiet pause ledger: load open pauses", "node_id", nodeID, "err", err)
		return nil
	}
	for _, p := range open {
		// The last report that saw it paused is unknown: closing it at its
		// start bills all of it unless a report proves more.
		start := ledgerTime(p.StartedAt)
		if cur, ok := node.open[p.InstanceID]; !ok || start.After(cur.start) {
			node.open[p.InstanceID] = openPause{start: start, lastSeen: start}
		}
	}
	node.loaded = true
	return node
}

// apply folds one node report into the pending writes.
func (n *ledgerNode) apply(report []InstanceStat, sampled time.Time) {
	seen := make(map[string]struct{}, len(report))
	for _, row := range report {
		seen[row.InstanceID] = struct{}{}
		rec := row.QuietPause
		rec.Since, rec.LastStarted, rec.LastEnded = ledgerTime(rec.Since), ledgerTime(rec.LastStarted), ledgerTime(rec.LastEnded)
		closed := !rec.LastStarted.IsZero() && !rec.LastEnded.Before(rec.LastStarted)
		if closed && !n.closed[row.InstanceID].Equal(rec.LastStarted) {
			n.addPending(row.InstanceID, rec.LastStarted, rec.LastEnded)
			n.closed[row.InstanceID] = rec.LastStarted
		}
		cur, hadOpen := n.open[row.InstanceID]
		if hadOpen && (!row.QuietPaused || !rec.Since.Equal(cur.start)) {
			// The open pause ended. Its completed report proves its end;
			// otherwise it ended unseen and is billed from its last sighting.
			if !closed || !rec.LastStarted.Equal(cur.start) {
				n.addPending(row.InstanceID, cur.start, cur.lastSeen)
			}
			delete(n.open, row.InstanceID)
		}
		if row.QuietPaused && !rec.Since.IsZero() {
			if o, ok := n.open[row.InstanceID]; ok && o.start.Equal(rec.Since) {
				o.lastSeen = sampled
				n.open[row.InstanceID] = o
				continue
			}
			n.open[row.InstanceID] = openPause{start: rec.Since, lastSeen: sampled}
			n.addPending(row.InstanceID, rec.Since, time.Time{})
		}
	}
	// A VM missing from its node's report is gone: its open pause ends at
	// the last report that saw it.
	for instance, o := range n.open {
		if _, ok := seen[instance]; !ok {
			n.addPending(instance, o.start, o.lastSeen)
			delete(n.open, instance)
		}
	}
	for instance := range n.closed {
		if _, ok := seen[instance]; !ok {
			delete(n.closed, instance)
		}
	}
}

// measure returns the quiet-paused time vmmd measured on the node between
// its previous report and this one: the growth of each incarnation's
// completed pauses plus how long its open pause has lasted. meterd's
// subtracted seconds come from the ledger built from the same reports, so
// they must stay under this (FaasQuietPauseUnbilledExceedsMeasured). An
// instance seen for the first time sets its baseline. A smaller completed
// total is a new incarnation, which started after the previous report, so
// all of its paused time is new.
func (n *ledgerNode) measure(report []InstanceStat, sampled time.Time) time.Duration {
	var grown time.Duration
	seen := make(map[string]struct{}, len(report))
	for _, row := range report {
		seen[row.InstanceID] = struct{}{}
		rec := row.QuietPause
		var cur pausedTally
		if rec.PausedTotal != nil {
			// vmmd reports the total once a pause has completed.
			cur.total = *rec.PausedTotal
		}
		if row.QuietPaused && !rec.Since.IsZero() && sampled.After(rec.Since) {
			cur.open = sampled.Sub(rec.Since)
		}
		prev, tracked := n.tallies[row.InstanceID]
		n.tallies[row.InstanceID] = cur
		switch {
		case !tracked:
		case cur.total < prev.total:
			grown += cur.total + cur.open
		default:
			// A completed pause moves its time from open to total; the
			// wall-clock open and the monotonic total can differ by a hair.
			grown += max(0, cur.total+cur.open-prev.total-prev.open)
		}
	}
	for instance := range n.tallies {
		if _, ok := seen[instance]; !ok {
			delete(n.tallies, instance)
		}
	}
	return grown
}

// ledgerTime is t at the ledger's precision. vmmd reports nanoseconds, but
// a pause is keyed by (instance, started_at) and timestamptz keeps
// microseconds (pgx truncates). Comparing a start read back from the ledger
// with the same start reported again must find them equal, and two keys must
// never collapse into one row inside a single write.
func ledgerTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Truncate(time.Microsecond)
}

// addPending queues a pause; a later end replaces an earlier one, and an
// open entry never replaces a close.
func (n *ledgerNode) addPending(instance string, start, end time.Time) {
	k := pauseKey{instance: instance, start: start}
	if prev, ok := n.pending[k]; ok && (end.IsZero() || (!prev.IsZero() && !end.After(prev))) {
		return
	}
	n.pending[k] = end
}

// flushLocked writes the node's pending pauses and advances its watermark,
// at most every flushEvery. A failed write keeps everything for the next
// attempt and leaves the watermark where it was, so billing stays as if
// the pauses never happened.
func (l *PauseLedger) flushLocked(ctx context.Context, nodeID string, node *ledgerNode, now time.Time) {
	if !node.lastFlush.IsZero() && now.Sub(node.lastFlush) < l.flushEvery {
		return
	}
	pauses := make([]state.QuietPause, 0, len(node.pending))
	for k, end := range node.pending {
		pauses = append(pauses, state.QuietPause{InstanceID: k.instance, StartedAt: k.start, EndedAt: end})
	}
	sort.Slice(pauses, func(i, j int) bool {
		if pauses[i].InstanceID != pauses[j].InstanceID {
			return pauses[i].InstanceID < pauses[j].InstanceID
		}
		return pauses[i].StartedAt.Before(pauses[j].StartedAt)
	})
	writeCtx, cancel := context.WithTimeout(ctx, pauseLedgerCallTimeout)
	err := l.store.RecordQuietPauses(writeCtx, nodeID, node.covered, pauses)
	cancel()
	if err != nil {
		l.metrics.write("error")
		l.log.Warn("quiet pause ledger: record", "node_id", nodeID, "pauses", len(pauses), "err", err)
		return
	}
	l.metrics.write("ok")
	l.metrics.watermark(nodeID, node.covered)
	node.pending = make(map[pauseKey]time.Time)
	node.lastFlush = now
}

// PauseLedgerMetrics owns the schedd_quiet_pause_ledger series. A nil value
// records nothing.
type PauseLedgerMetrics struct {
	writes     *prometheus.CounterVec
	watermarks *prometheus.GaugeVec
	measuredS  *prometheus.CounterVec
}

// NewPauseLedgerMetrics registers schedd_quiet_pause_ledger_writes_total,
// schedd_quiet_pause_ledger_watermark_seconds and
// schedd_quiet_pause_measured_seconds_total on reg.
func NewPauseLedgerMetrics(reg prometheus.Registerer) *PauseLedgerMetrics {
	m := &PauseLedgerMetrics{
		writes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_quiet_pause_ledger_writes_total",
			Help: "Quiet pause ledger writes by outcome (ok, error, load_error), ADR-20261023171905.",
		}, []string{"outcome"}),
		watermarks: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "schedd_quiet_pause_ledger_watermark_seconds",
			Help: "Unix time of the newest node report the quiet pause ledger has recorded. meterd counts an open pause only up to it; it stops moving while a node reports no VM.",
		}, []string{"node"}),
		measuredS: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_quiet_pause_measured_seconds_total",
			Help: "Quiet-paused seconds vmmd measured on the node (completed pauses plus open pause time), from the reports the pause ledger is built from. meterd_quiet_pause_unbilled_seconds_total must stay under it (ADR-20261023171905).",
		}, []string{"node"}),
	}
	for _, outcome := range []string{"ok", "error", "load_error"} {
		m.writes.WithLabelValues(outcome)
	}
	if reg != nil {
		reg.MustRegister(m.writes, m.watermarks, m.measuredS)
	}
	return m
}

func (m *PauseLedgerMetrics) measured(nodeID string, d time.Duration) {
	if m != nil {
		m.measuredS.WithLabelValues(nodeID).Add(d.Seconds())
	}
}

func (m *PauseLedgerMetrics) write(outcome string) {
	if m != nil {
		m.writes.WithLabelValues(outcome).Inc()
	}
}

func (m *PauseLedgerMetrics) watermark(nodeID string, covered time.Time) {
	if m != nil {
		m.watermarks.WithLabelValues(nodeID).Set(float64(covered.UnixNano()) / 1e9)
	}
}
