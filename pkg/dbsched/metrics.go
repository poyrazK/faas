package dbsched

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the database scheduler's metrics. The counters count this
// replica's events. The catalog gauges come from the shared store, which
// every replica's idle sweep observes, so alerts take max across
// replicas. No series carries a database or account id; node ids are the
// fleet's database nodes. A nil *Metrics records nothing.
type Metrics struct {
	failovers    *prometheus.CounterVec
	standbyDrops *prometheus.CounterVec
	// standbyResumes counts wakes that resumed a synchronized standby.
	standbyResumes *prometheus.CounterVec
	moves          *prometheus.CounterVec
	storage        *prometheus.GaugeVec
	stale          prometheus.Gauge
	staleRestart   *prometheus.CounterVec
	drills         *prometheus.CounterVec
	drillLastOK    *prometheus.GaugeVec
	upgrades       *prometheus.CounterVec
	catalog        *catalogCollector
}

// Failover triggers: the operator marked the node lost, the standby
// monitor found a running or a parked database's node silent, or a
// planned switchover (switchover.go) handed the database over.
const (
	failoverLostNode   = "lost_node"
	failoverRunning    = "running"
	failoverParked     = "parked"
	failoverSwitchover = "switchover"
)

// Standby drop reasons: it stopped keeping up, its node is going away, or
// an attach died before it became synchronous.
const (
	dropLagging    = "lagging"
	dropNodeGone   = "node_gone"
	dropStaleClaim = "stale_claim"
)

// NewMetrics registers the scheduler's metrics on reg, named prefix +
// "_database_…" (schedd passes its ops prefix, "schedd").
func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	m := &Metrics{
		failovers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_failovers_total",
			Help: "Failovers of replicated databases to their standbys, by trigger (lost_node, running, parked, switchover: planned, losing nothing) and result (ok, failed).",
		}, []string{"trigger", "result"}),
		standbyDrops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_standby_drops_total",
			Help: "Standbys given up while their primary ran, by reason (lagging, node_gone, stale_claim). The primary commits without a standby until one attaches again.",
		}, []string{"reason"}),
		standbyResumes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_standby_resumes_total",
			Help: "Wakes of replicated databases that resumed their synchronized standby, so commits waited for it from the first one, by result (ok; failed: the wake went on asynchronous).",
		}, []string{"result"}),
		moves: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_moves_total",
			Help: "Database moves between nodes by result (ok, failed).",
		}, []string{"result"}),
		storage: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_database_storage",
			Help: "Running native databases at the last storage sample, by level: sampled, low (short of the space that would end read-only), read_only (the storage guard turned the primary read-only), standby_low (the standby is low while its primary is not).",
		}, []string{"level"}),
		stale: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_database_stale_vms",
			Help: "Running native databases on an outdated base (PostgreSQL minor, guest-init) or launch at the last sample, their standbys included; only a restart replaces either.",
		}),
		staleRestart: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_stale_restarts_total",
			Help: "Idle always-on databases restarted onto the current base, by result (ok, failed).",
		}, []string{"result"}),
		drills: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_restore_drills_total",
			Help: "Restore drills by result: ok (a database restored to a minute before the drill from its node's backups), failed, or skipped (no capacity to start one).",
		}, []string{"result"}),
		drillLastOK: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_database_restore_drill_last_ok_timestamp_seconds",
			Help: "When a database node's last restore drill passed (0: none since its drills began). Only nodes with drills report.",
		}, []string{"node"}),
		upgrades: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_database_upgrades_total",
			Help: "In-place major upgrades by result: ok, refused (pg_upgrade's check refused the cluster, which stays on its major), failed (the database needs a restore).",
		}, []string{"result"}),
		catalog: newCatalogCollector(prefix),
	}
	reg.MustRegister(m.failovers, m.standbyDrops, m.standbyResumes, m.moves, m.storage, m.stale, m.staleRestart, m.drills, m.drillLastOK, m.upgrades, m.catalog)
	return m
}

func (m *Metrics) failedOver(trigger string, err error) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "failed"
	}
	m.failovers.WithLabelValues(trigger, result).Inc()
}

func (m *Metrics) resumedStandby(ok bool) {
	if m == nil {
		return
	}
	result := "ok"
	if !ok {
		result = "failed"
	}
	m.standbyResumes.WithLabelValues(result).Inc()
}

func (m *Metrics) standbyDropped(reason string) {
	if m != nil {
		m.standbyDrops.WithLabelValues(reason).Inc()
	}
}

func (m *Metrics) sampledStorage(s StorageSample) {
	if m == nil {
		return
	}
	m.storage.WithLabelValues("sampled").Set(float64(s.Sampled))
	m.storage.WithLabelValues("low").Set(float64(s.Low))
	m.storage.WithLabelValues("read_only").Set(float64(s.ReadOnly))
	m.storage.WithLabelValues("standby_low").Set(float64(s.StandbyLow))
	m.stale.Set(float64(s.Stale))
}

func (m *Metrics) drilled(result DrillResult) {
	if m != nil {
		m.drills.WithLabelValues(string(result)).Inc()
	}
}

func (m *Metrics) observedDrills(summaries map[string]DrillSummary) {
	if m == nil {
		return
	}
	for node, sum := range summaries {
		at := 0.0
		if !sum.LastOK.IsZero() {
			at = float64(sum.LastOK.Unix())
		}
		m.drillLastOK.WithLabelValues(node).Set(at)
	}
}

func (m *Metrics) restartedStale(err error) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "failed"
	}
	m.staleRestart.WithLabelValues(result).Inc()
}

func (m *Metrics) upgraded(result string) {
	if m != nil {
		m.upgrades.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) moved(err error) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "failed"
	}
	m.moves.WithLabelValues(result).Inc()
}

// catalog is one observation of the shared store.
type catalog struct {
	vms map[State]int
	// standbys counts replicated databases by state (running, parked)
	// and standby status (synced, unsynced, none).
	standbys map[[2]string]int
	nodes    []nodeCatalog
}

type nodeCatalog struct {
	node Node
	load NodeLoad
}

// standbyStatus is a replicated database's standby: synced (it may be
// promoted), unsynced, or none placed.
func standbyStatus(vm VM) string {
	switch {
	case vm.StandbyNode == "":
		return "none"
	case vm.StandbySynced:
		return "synced"
	default:
		return "unsynced"
	}
}

// allStates are every database state, so each is reported, zero or not.
var allStates = []State{StateProvisioning, StateParked, StateWaking, StateRunning, StateParking, StateMoving, StateUpgrading, StateDeleting, StateFailed}

// Observe refreshes the catalog gauges from the store. The idle sweep calls
// it; without metrics it does nothing.
func (s *Scheduler) Observe(ctx context.Context) error {
	if s.cfg.Metrics == nil {
		return nil
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return err
	}
	loads, err := s.store.Loads(ctx)
	if err != nil {
		return err
	}
	vms, err := s.store.List(ctx, allStates...)
	if err != nil {
		return err
	}
	c := catalog{vms: map[State]int{}, standbys: map[[2]string]int{}}
	for _, vm := range vms {
		c.vms[vm.State]++
		if vm.Replicated && (vm.State == StateRunning || vm.State == StateParked) {
			c.standbys[[2]string{string(vm.State), standbyStatus(vm)}]++
		}
	}
	for _, n := range nodes {
		c.nodes = append(c.nodes, nodeCatalog{node: n, load: loads[n.ID]})
	}
	s.cfg.Metrics.catalog.set(c)
	return nil
}

// catalogCollector reports the last observation whole, so a node removed
// from the configuration leaves no stale series.
type catalogCollector struct {
	vms, standbys                     *prometheus.Desc
	memory, ceiling, volume, capacity *prometheus.Desc
	lifecycle                         *prometheus.Desc
	mu                                sync.Mutex
	last                              *catalog
}

func newCatalogCollector(prefix string) *catalogCollector {
	node := []string{"node"}
	return &catalogCollector{
		vms: prometheus.NewDesc(prefix+"_database_vms",
			"Native databases by state.", []string{"state"}, nil),
		standbys: prometheus.NewDesc(prefix+"_database_standbys",
			"Replicated (production-class) databases by state (running, parked) and standby status: synced (promotable), unsynced, or none placed.", []string{"state", "status"}, nil),
		memory: prometheus.NewDesc(prefix+"_database_node_memory_mb",
			"Memory admitted on a database node: active database VMs and running standbys, with per-VM overhead.", node, nil),
		ceiling: prometheus.NewDesc(prefix+"_database_node_memory_ceiling_mb",
			"A database node's admission ceiling.", node, nil),
		volume: prometheus.NewDesc(prefix+"_database_node_volume_bytes",
			"Volume committed on a database node: its databases, standbys and incoming moves.", node, nil),
		capacity: prometheus.NewDesc(prefix+"_database_node_volume_capacity_bytes",
			"A database node's volume capacity.", node, nil),
		lifecycle: prometheus.NewDesc(prefix+"_database_node_lifecycle",
			"1 for a database node's lifecycle (active, draining, unavailable).", []string{"node", "lifecycle"}, nil),
	}
}

func (c *catalogCollector) set(next catalog) {
	c.mu.Lock()
	c.last = &next
	c.mu.Unlock()
}

func (c *catalogCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.vms, c.standbys, c.memory, c.ceiling, c.volume, c.capacity, c.lifecycle} {
		ch <- d
	}
}

func (c *catalogCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	last := c.last
	c.mu.Unlock()
	if last == nil {
		return // not observed yet
	}
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	for _, state := range allStates {
		gauge(c.vms, float64(last.vms[state]), string(state))
	}
	for _, state := range []State{StateRunning, StateParked} {
		for _, status := range []string{"synced", "unsynced", "none"} {
			gauge(c.standbys, float64(last.standbys[[2]string{string(state), status}]), string(state), status)
		}
	}
	for _, n := range last.nodes {
		gauge(c.memory, float64(n.load.ActiveMB), n.node.ID)
		gauge(c.ceiling, float64(n.node.AdmissionCeilingMB), n.node.ID)
		gauge(c.volume, float64(n.load.VolumeBytes), n.node.ID)
		gauge(c.capacity, float64(n.node.VolumeCapacityBytes), n.node.ID)
		gauge(c.lifecycle, 1, n.node.ID, n.node.Lifecycle)
	}
}
