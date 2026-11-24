package agentd

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Metrics is agentd's Prometheus surface for runs and deliveries
// (ADR-20261027090010 §12 additions). Labels are bounded: run states, start
// stages, gRPC codes, destinations, delivery states and attempt results.
// Nothing a guest reports becomes a label.
type Metrics struct {
	runs             *prometheus.CounterVec
	requeues         *prometheus.CounterVec
	startSeconds     *prometheus.HistogramVec
	runSeconds       prometheus.Histogram
	queueDepth       prometheus.Gauge
	queueOldest      prometheus.Gauge
	deliveries       *prometheus.CounterVec
	deliveryAttempts *prometheus.CounterVec
	attemptSeconds   *prometheus.HistogramVec
	memory           *prometheus.CounterVec
	sampleGap        prometheus.Histogram
	// sandboxBilled and sandboxPaused are a session VM's measured usage
	// (ADR-20261027090013 step 11).
	sandboxBilled prometheus.Counter
	sandboxPaused prometheus.Counter
	// deadlineCredit is approval-wait credit sent to vmmd
	// (ADR-20261027090016 amendment 3).
	deadlineCredit prometheus.Counter
	// Persistent session pauses and resumes (ADR-20261122132055, Metrics).
	sandboxPause       prometheus.Histogram
	sandboxResume      *prometheus.HistogramVec
	sandboxResumedCold *prometheus.CounterVec
	// Workspace volume backups taken while sessions run
	// (ADR-20261122132055 §4, Metrics).
	volumeBackupSeconds prometheus.Histogram
	volumeBackupBytes   prometheus.Histogram
	// Forks (ADR-20261122132055 §3, Metrics).
	forkSeconds *prometheus.HistogramVec
	forks       *prometheus.CounterVec
}

// Outcomes of sandbox_forks_total: a fork restored on its capture's node, or
// on another from the shared store, or cold-booted; refused on the
// capture's node (it then waits for the upload); or failed because its
// source ended before the capture.
const (
	forkNode     = "node"
	forkRemote   = "remote"
	forkCold     = "cold"
	forkRefused  = "refused_node"
	forkOrphaned = "orphaned"
)

// forkCountBucket bounds sandbox_fork_seconds' label: how many forks one
// capture serves.
func forkCountBucket(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 4:
		return "2-4"
	default:
		return "5+"
	}
}

// Sources of sandbox_session_resume_seconds: the capture restored on its
// own node or another, or the session cold-booted without it.
const (
	resumeNode   = "node"
	resumeRemote = "remote"
	resumeCold   = "cold"
)

// Measures of agent_run_memory_gb_seconds_total (ADR-20261027090011).
const (
	measurePrivate         = "private"          // sampled private memory
	measureUnpausedPrivate = "unpaused_private" // the part while the VM ran
	measureBillable        = "billable"         // unpaused, at the floor and per-VM overhead
	measureConfigured      = "configured"       // ram_mb + 8 MB over the measured time, today's rule
)

// Start stages of agent_run_start_seconds.
const (
	stageQueue = "queue" // queued until a replica claims it
	stageInput = "input" // the checkout prepared on the input drive
	stagePlace = "place" // schedd's admission on the agent pool
	stageBoot  = "boot"  // vmmd's boot, as schedd reports it
)

// NewMetrics registers agentd's metrics on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("agentd: nil prometheus registerer")
	}
	m := &Metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_runs_total",
			Help: "Agent runs this replica finished, by terminal state (succeeded, failed, timed_out, out_of_memory, cancelled).",
		}, []string{"outcome"}),
		requeues: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_run_requeues_total",
			Help: "Claimed agent runs queued again before reaching a VM, by the gRPC code of the cause.",
		}, []string{"cause"}),
		startSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_run_start_seconds",
			Help:    "Time an agent run spent in one start stage (queue, input, place, boot).",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900},
		}, []string{"stage"}),
		runSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "agent_run_duration_seconds",
			Help:    "Time from an agent run's placement on the agent pool to its terminal state.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600},
		}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "agent_run_queue_depth",
			Help: "Queued agent runs across every account, as this replica last read them.",
		}),
		queueOldest: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "agent_run_queue_oldest_seconds",
			Help: "How long the oldest queued agent run has waited, as this replica last read it; 0 when none is queued.",
		}),
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_deliveries_total",
			Help: "Agent run deliveries that ended, by destination and final state.",
		}, []string{"destination", "outcome"}),
		deliveryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_delivery_attempts_total",
			Help: "Agent run delivery attempts, by destination and result (delivered, retry, rejected, unknown).",
		}, []string{"destination", "result"}),
		attemptSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_delivery_attempt_seconds",
			Help:    "Duration of one agent run delivery attempt, by destination.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 20, 40},
		}, []string{"destination"}),
		memory: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_run_memory_gb_seconds_total",
			Help: "Agent run VM memory vmmd measured, in GB-seconds, by measure: private, unpaused_private, billable (the ADR-20261027090011 rule) or configured (ram_mb + 8 MB). Not billed.",
		}, []string{"measure"}),
		sampleGap: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "agent_run_sample_gap_ratio",
			Help:    "Share of an agent run's memory samples vmmd could not read (ADR-20261027090011).",
			Buckets: []float64{0, .01, .05, .1, .2, .5, 1},
		}),
		sandboxBilled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sandbox_session_billed_gb_seconds_total",
			Help: "Sandbox session VM memory vmmd measured under the ADR-20261027090011 rule, in GB-seconds: unpaused, unfrozen seconds at max(private, the floor) + 8 MB. Not billed yet.",
		}),
		sandboxPaused: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sandbox_session_paused_seconds_total",
			Help: "Seconds sandbox session VMs spent quiet-paused or frozen, which are not billed (ADR-20261027090013 §5).",
		}),
		deadlineCredit: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "agent_run_deadline_credit_seconds_total",
			Help: "Seconds agent run deadlines grew by while a person decided an approval (ADR-20261027090016 amendment 3).",
		}),
		sandboxPause: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sandbox_session_pause_seconds",
			Help:    "Time vmmd took to capture a persistent sandbox session and end its VM (ADR-20261122132055 §2).",
			Buckets: []float64{.25, .5, 1, 2, 5, 10, 30, 60, 120},
		}),
		sandboxResume: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sandbox_session_resume_seconds",
			Help:    "Time from a paused sandbox session's claim to its VM up, by source: node (its capture on its own node), remote (another node) or cold (no capture restored).",
			Buckets: []float64{.25, .5, 1, 2, 5, 10, 30, 60, 120},
		}, []string{"source"}),
		volumeBackupSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sandbox_volume_backup_seconds",
			Help:    "Time vmmd took to back up a running persistent sandbox session's workspace volume (ADR-20261122132055 §4).",
			Buckets: []float64{.1, .25, .5, 1, 2, 5, 10, 30, 60, 120, 300},
		}),
		volumeBackupBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sandbox_volume_backup_bytes",
			Help:    "Compressed bytes one workspace volume backup added to the store: the chunks it had not held (ADR-20261122132055 §4).",
			Buckets: prometheus.ExponentialBuckets(4096, 4, 12),
		}),
		sandboxResumedCold: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sandbox_resumed_cold_total",
			Help: "Paused sandbox sessions that resumed by cold boot, losing their memory state, by reason: no_capture or restore_failed (ADR-20261122132055 §2).",
		}, []string{"reason"}),
	}
	m.forkSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sandbox_fork_seconds",
		Help:    "Time from agentd asking for a running sandbox session's fork capture to vmmd recording it, by how many forks it serves (1, 2-4, 5+) (ADR-20261122132055 §3).",
		Buckets: []float64{.25, .5, 1, 2, 5, 10, 30, 60},
	}, []string{"count_bucket"})
	m.forks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sandbox_forks_total",
		Help: "Sandbox fork starts by outcome: node (restored on its capture's node), remote (from the shared store), cold (cold-booted), refused_node (refused on the capture's node, waits for the upload) or orphaned (its source ended before the capture) (ADR-20261122132055 §3).",
	}, []string{"outcome"})
	for _, b := range []string{"1", "2-4", "5+"} {
		m.forkSeconds.WithLabelValues(b)
	}
	for _, o := range []string{forkNode, forkRemote, forkCold, forkRefused, forkOrphaned} {
		m.forks.WithLabelValues(o)
	}
	for _, source := range []string{resumeNode, resumeRemote, resumeCold} {
		m.sandboxResume.WithLabelValues(source)
	}
	for _, measure := range []string{measurePrivate, measureUnpausedPrivate, measureBillable, measureConfigured} {
		m.memory.WithLabelValues(measure)
	}
	for _, c := range []prometheus.Collector{m.runs, m.requeues, m.startSeconds, m.runSeconds, m.queueDepth, m.queueOldest,
		m.deliveries, m.deliveryAttempts, m.attemptSeconds, m.memory, m.sampleGap, m.sandboxBilled, m.sandboxPaused, m.deadlineCredit,
		m.sandboxPause, m.sandboxResume, m.sandboxResumedCold, m.volumeBackupSeconds, m.volumeBackupBytes,
		m.forkSeconds, m.forks} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// stage records the time a run spent in one start stage.
func (m *Metrics) stage(name string, took time.Duration) {
	if m == nil || took < 0 {
		return
	}
	m.startSeconds.WithLabelValues(name).Observe(took.Seconds())
}

// finished counts a run that reached a terminal state.
func (m *Metrics) finished(state string) {
	if m == nil {
		return
	}
	switch state {
	case agentstore.StateSucceeded, agentstore.StateFailed, agentstore.StateTimedOut, agentstore.StateOutOfMemory, agentstore.StateCancelled:
		m.runs.WithLabelValues(state).Inc()
	}
}

// ran records how long a placed run took to reach its terminal state.
func (m *Metrics) ran(took time.Duration) {
	if m == nil {
		return
	}
	m.runSeconds.Observe(took.Seconds())
}

// used adds a run's measured memory, beside what configured-RAM billing
// would count for the same time.
func (m *Metrics) used(u agentstore.Usage, ramMB int) {
	if m == nil {
		return
	}
	const mbMillisPerGBSecond = 1024 * 1000
	m.memory.WithLabelValues(measurePrivate).Add(float64(u.PrivateMBMillis) / mbMillisPerGBSecond)
	m.memory.WithLabelValues(measureUnpausedPrivate).Add(float64(u.UnpausedPrivateMBMillis) / mbMillisPerGBSecond)
	m.memory.WithLabelValues(measureBillable).Add(float64(u.BillableMBMillis) / mbMillisPerGBSecond)
	if ramMB > 0 {
		m.memory.WithLabelValues(measureConfigured).Add(float64(int64(ramMB+api.PerVMOverheadMB)*u.MeasuredMillis) / mbMillisPerGBSecond)
	}
	if u.ExpectedSamples > 0 {
		m.sampleGap.Observe(1 - float64(u.Samples)/float64(u.ExpectedSamples))
	}
}

// requeued counts a claimed run that went back to the queue.
func (m *Metrics) requeued(cause string) {
	if m == nil {
		return
	}
	m.requeues.WithLabelValues(cause).Inc()
}

// queue records the queue's depth and the oldest queued run's wait.
func (m *Metrics) queue(depth int, oldest time.Duration) {
	if m == nil {
		return
	}
	m.queueDepth.Set(float64(depth))
	m.queueOldest.Set(oldest.Seconds())
}

// attempted records one delivery attempt and, when it ended the delivery,
// its final state.
func (m *Metrics) attempted(destination string, result agentstore.DeliveryResult, state string, took time.Duration) {
	if m == nil {
		return
	}
	m.deliveryAttempts.WithLabelValues(destination, attemptResult(result.Outcome)).Inc()
	m.attemptSeconds.WithLabelValues(destination).Observe(took.Seconds())
	m.ended(agentstore.DeliveryEnd{Destination: destination, State: state})
}

// ended counts a delivery that reached a final state.
func (m *Metrics) ended(e agentstore.DeliveryEnd) {
	if m == nil {
		return
	}
	switch e.State {
	case api.AgentDeliveryDelivered, api.AgentDeliveryFailed, api.AgentDeliveryOutcomeUnknown, api.AgentDeliverySuperseded, api.AgentDeliveryCancelled:
		m.deliveries.WithLabelValues(e.Destination, e.State).Inc()
	}
}

func attemptResult(o agentstore.DeliveryOutcome) string {
	switch o {
	case agentstore.DeliveryDelivered:
		return "delivered"
	case agentstore.DeliveryRetry:
		return "retry"
	case agentstore.DeliveryRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// sandboxUsed counts a session's growth from one cumulative usage record to
// the next.
func (m *Metrics) sandboxUsed(prev, next agentstore.SandboxUsage) {
	if m == nil {
		return
	}
	if d := next.BillableMBMillis - prev.BillableMBMillis; d > 0 {
		m.sandboxBilled.Add(float64(d) / 1024 / 1000)
	}
	if d := next.PausedMillis - prev.PausedMillis; d > 0 {
		m.sandboxPaused.Add(float64(d) / 1000)
	}
}

// sandboxPausedIn records one pause's capture time.
func (m *Metrics) sandboxPausedIn(took time.Duration) {
	if m == nil || took < 0 {
		return
	}
	m.sandboxPause.Observe(took.Seconds())
}

// volumeBackedUp records one backup of a running session's volume.
func (m *Metrics) volumeBackedUp(took time.Duration, uploaded int64) {
	if m == nil {
		return
	}
	m.volumeBackupSeconds.Observe(took.Seconds())
	m.volumeBackupBytes.Observe(float64(uploaded))
}

// sandboxResumed records one resume by its source, and why a cold one was.
func (m *Metrics) sandboxResumed(source, coldReason string, took time.Duration) {
	if m == nil {
		return
	}
	if took >= 0 {
		m.sandboxResume.WithLabelValues(source).Observe(took.Seconds())
	}
	if source == resumeCold {
		m.sandboxResumedCold.WithLabelValues(coldReason).Inc()
	}
}

// sandboxForked records one fork capture serving forks.
func (m *Metrics) sandboxForked(forks int, took time.Duration) {
	if m == nil || took < 0 {
		return
	}
	m.forkSeconds.WithLabelValues(forkCountBucket(forks)).Observe(took.Seconds())
}

// forkOutcome counts n fork starts with outcome.
func (m *Metrics) forkOutcome(outcome string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.forks.WithLabelValues(outcome).Add(float64(n))
}

func (m *Metrics) creditGranted(d time.Duration) {
	if m == nil {
		return
	}
	m.deadlineCredit.Add(d.Seconds())
}
