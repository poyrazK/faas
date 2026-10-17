package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

type diskReplicationWaitMetrics struct {
	slots             chan struct{}
	waiting           prometheus.Gauge
	waits             *prometheus.HistogramVec
	ctx               context.Context
	cancel            context.CancelFunc
	transfers         chan struct{}
	transferring      prometheus.Gauge
	rejectedTransfers prometheus.Counter
	entryBatches      chan struct{}
}

func newDiskReplicationWaitMetrics(registry *prometheus.Registry) *diskReplicationWaitMetrics {
	m := &diskReplicationWaitMetrics{
		slots:   make(chan struct{}, api.ObjectNodeReplicationMaxWaiters),
		waiting: prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_replication_waiting", Help: "Authenticated replication change requests currently waiting; bounded independently of S3 writes."}),
		waits:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "faas_object_node_replication_change_wait_seconds", Help: "Replication change request duration by bounded outcome: changed, timeout, canceled, error or rejected."}, []string{"outcome"}),
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.transfers = make(chan struct{}, api.ObjectNodeReplicationMaxTransfers)
	m.entryBatches = make(chan struct{}, api.ObjectNodeReplicationMaxEntryBatches)
	m.transferring = prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_replication_transfers", Help: "Active bounded replication payload responses."})
	m.rejectedTransfers = prometheus.NewCounter(prometheus.CounterOpts{Name: "faas_object_node_replication_transfer_rejections_total", Help: "Payload requests refused because the transfer slots were full."})
	registry.MustRegister(m.waiting, m.waits)
	registry.MustRegister(m.transferring, m.rejectedTransfers)
	return m
}

// CancelReplicationWaits releases idle replication requests during graceful
// listener shutdown without canceling ordinary in-flight S3 operations.
func (h *DiskHandler) CancelReplicationWaits() { h.metrics.replication.cancel() }

func (h *DiskHandler) replicationTransfer(w http.ResponseWriter, r *http.Request, id string) func() {
	m := h.metrics.replication
	select {
	case m.transfers <- struct{}{}:
		m.transferring.Inc()
		return func() { m.transferring.Dec(); <-m.transfers }
	default:
		m.rejectedTransfers.Inc()
		h.failure(w, r, id, objectnode.ErrCapacity)
		return nil
	}
}

func (h *DiskHandler) replicationWait(w http.ResponseWriter, r *http.Request, id string) {
	q := operationQuery(r.URL.Query())
	sequence, err := replicationNumber(q.Get("sequence"), 0, api.ObjectNodeReplicationMaxSequence)
	epoch, epochErr := replicationNumber(q.Get("epoch"), 1, api.ObjectNodeReplicationMaxEpoch)
	if err != nil || epochErr != nil || !diskMultipartQuery(q, objectnode.ReplicationQuery, "stream", "primary", "epoch", "sequence", "hash", "revision") {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	cursor := objectnode.ReplicationWaitCursor{StreamID: q.Get("stream"), PrimaryID: q.Get("primary"), Epoch: epoch, Sequence: sequence, Hash: q.Get("hash"), Revision: q.Get("revision")}
	m := h.metrics.replication
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		m.waits.WithLabelValues("rejected").Observe(0)
		h.failure(w, r, id, objectnode.ErrCapacity)
		return
	}
	m.waiting.Inc()
	defer m.waiting.Dec()
	started := time.Now()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel) //nolint:contextcheck // Listener shutdown adds cancellation to the request-derived context.
	defer stop()
	status, err := h.config.Store.WaitForReplicationChange(ctx, cursor)
	outcome := "timeout"
	if err != nil {
		outcome = "error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	} else if current, cursorErr := objectnode.NewReplicationWaitCursor(status); cursorErr == nil && current != cursor {
		outcome = "changed"
	}
	m.waits.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
	h.inventoryResult(w, r, id, status, err)
}
