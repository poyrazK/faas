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
		oldest: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_inventory_oldest_observation_age_seconds", Help: "Age of the oldest bucket inventory admission still holds to the 900 s window, read after each inventory sweep. Buckets held by a configuration change or listing within the progress window are left out; class=active (quiet buckets join with ADR-20261110151628 step 4)."}, []string{"class"}),
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
	backlog, err := st.ObjectInventoryBacklog(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.Host.Logger().Warn("object storage inventory backlog read failed")
		}
		return
	}
	m.due.Set(float64(backlog.DueBuckets))
	m.oldest.WithLabelValues("active").Set(backlog.OldestObservationAge.Seconds())
}
