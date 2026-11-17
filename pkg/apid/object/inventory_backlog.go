package object

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/state"
)

// inventoryBacklogMetrics show how far the inventory sweep is behind before
// admission starts refusing transfers at 900 s (ADR-20261110151628 §3).
type inventoryBacklogMetrics struct {
	due    prometheus.Gauge
	oldest *prometheus.GaugeVec
}

func newInventoryBacklogMetrics() inventoryBacklogMetrics {
	return inventoryBacklogMetrics{
		due:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_storage_inventory_due_buckets", Help: "Ready buckets due for an inventory listing, read after each inventory sweep (ADR-20261110151628)."}),
		oldest: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_inventory_oldest_observation_age_seconds", Help: "Age of the oldest bucket inventory admission relies on, read after each inventory sweep (ADR-20261110151628). class=active: buckets held to the 900 s window, counted from when a bucket turned active if it was quiet on its observation; buckets held by a configuration change or listing within the progress window are left out. class=quiet: buckets held to 24 h with object_inventory_quiet on, 0 otherwise."}, []string{"class"}),
	}
}

func (m inventoryBacklogMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.due, m.oldest}
}

// observeInventoryBacklog reads the sweep's backlog into m. A failed read
// keeps the last values: a failing store also fails the sweep, which
// FaasObjectStorageInventorySweepStopped reports.
func (s *Service) observeInventoryBacklog(ctx context.Context, m inventoryBacklogMetrics) {
	st, ok := s.Host.Store().(state.ObjectInventoryBacklogStore)
	if !ok || s.Host.ObjectStorage() == nil {
		return
	}
	backlog, err := st.ObjectInventoryBacklog(ctx, s.Host.ObjectStorage().Accounting.GatewaySafety())
	if err != nil {
		if ctx.Err() == nil {
			s.Host.Logger().Warn("object storage inventory backlog read failed")
		}
		return
	}
	m.due.Set(float64(backlog.DueBuckets))
	m.oldest.WithLabelValues("active").Set(backlog.OldestObservationAge.Seconds())
	m.oldest.WithLabelValues("quiet").Set(backlog.OldestQuietObservationAge.Seconds())
}

func newInventoryDriftCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_inventory_drift_total", Help: "Verification listings of quiet buckets (nothing admitted since the previous listing started) that counted more (up) or less (down) than that listing, with object_inventory_quiet shadow or on (ADR-20261110151628). Something wrote around admission."}, []string{"direction"})
}

// recordInventoryVerification counts and logs a quiet bucket whose listing
// changed. Only gateway-safety accounting makes admission the only door.
func (s *Service) recordInventoryVerification(b state.ObjectBucket, v state.ObjectInventoryVerification) {
	direction := v.Drift()
	if direction == "" || !s.Host.ObjectStorage().Accounting.GatewaySafety() {
		return
	}
	if drift := s.inventoryDrift.Load(); drift != nil {
		drift.WithLabelValues(direction).Inc()
	}
	s.Host.Logger().Warn("object storage inventory drift on a quiet bucket", "bucket_id", b.ID, "backend_id", b.BackendID, "direction", direction,
		"previous_bytes", v.PreviousBytes, "previous_keys", v.PreviousKeys, "listed_bytes", v.Bytes, "listed_keys", v.Keys)
}
