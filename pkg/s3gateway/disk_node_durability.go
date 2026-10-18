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

// replicaResponse withholds successful S3/receipt/inventory headers until the
// captured final journal position is confirmed. Conditional rejection also waits
// because its terminal negative receipt is durable recovery evidence. Replication
// export and confirmation RPCs bypass this gate so they can make progress.
type replicaResponse struct {
	http.ResponseWriter
	handler *DiskHandler
	request *http.Request
	id      string
	status  int
	failed  bool
}

func (w *replicaResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *replicaResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status < http.StatusOK {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if status >= 200 && status < 300 || status == http.StatusPreconditionFailed {
		if err := w.handler.awaitReplica(w.request.Context()); err != nil {
			w.failed = true
			// Remove object length, checksum, receipt and range headers before
			// emitting an uncertain outcome instead of the prepared response.
			for name := range w.Header() {
				w.Header().Del(name)
			}
			w.Header().Set("X-Amz-Request-Id", w.id)
			w.Header().Set("Cache-Control", "no-store")
			w.handler.failure(w.ResponseWriter, w.request, w.id, err)
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *replicaResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return 0, objectnode.ErrReplicaPending
	}
	return w.ResponseWriter.Write(body)
}

func (h *DiskHandler) awaitReplica(ctx context.Context) error {
	policy, _, err := h.config.Store.DurabilityStatus()
	if err != nil || policy.Mode == objectnode.DurabilityLocal {
		return err
	}
	h.metrics.replica.waiting.Inc()
	defer h.metrics.replica.waiting.Dec()
	started := time.Now()
	wait, cancel := context.WithTimeout(ctx, api.ObjectNodeReplicaWaitTimeout)
	defer cancel()
	err = h.config.Store.WaitForReplica(wait)
	outcome := "confirmed"
	if errors.Is(err, objectnode.ErrReplicaPending) {
		outcome = "pending"
	} else if err != nil {
		outcome = "error"
	}
	h.metrics.replica.waits.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
	return err
}

type diskReplicaMetrics struct {
	waiting prometheus.Gauge
	waits   *prometheus.HistogramVec
}

func newDiskReplicaMetrics(h *DiskHandler, registry *prometheus.Registry) *diskReplicaMetrics {
	m := &diskReplicaMetrics{
		waiting: prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_replica_waiting_responses", Help: "Responses currently awaiting replica confirmation."}),
		waits:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "faas_object_node_replica_wait_seconds", Help: "Time spent waiting for replica durability, by bounded outcome."}, []string{"outcome"}),
	}
	registry.MustRegister(m.waiting, m.waits)
	for _, kind := range []string{"required", "bound", "confirmation_known", "lag_records", "caught_up"} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_replica_" + kind, Help: "Memory-only replica durability observation: " + kind + "; no node or stream identity labels."}, func() float64 { return replicaMetricValue(h.config.Store, kind) }))
	}
	return m
}

func replicaMetricValue(store *objectnode.Store, kind string) float64 {
	p, sequence, err := store.DurabilityStatus()
	if err != nil || p.Mode != objectnode.DurabilityReplica {
		return 0
	}
	switch kind {
	case "required":
		return 1
	case "bound":
		if p.ReplicaID != "" {
			return 1
		}
	case "confirmation_known":
		if p.ConfirmedSequence >= 0 {
			return 1
		}
	case "lag_records":
		return float64(sequence - max(p.ConfirmedSequence, 0))
	case "caught_up":
		if p.ConfirmedSequence >= sequence {
			return 1
		}
	}
	return 0
}
