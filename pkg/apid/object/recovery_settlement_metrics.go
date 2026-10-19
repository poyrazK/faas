package object

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type nodeSettlementMetrics struct {
	backlog *prometheus.GaugeVec
	oldest  prometheus.Gauge
}

func newNodeSettlementMetrics() nodeSettlementMetrics {
	return nodeSettlementMetrics{
		backlog: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_settlement_backlog", Help: "Durable owned-node acknowledgement jobs by waiting or ready state, including leased and deferred jobs."}, []string{"state"}),
		oldest:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_storage_settlement_oldest_ready_seconds", Help: "Age of the oldest undelivered terminal acknowledgement, including deferred jobs."}),
	}
}

func (s *Service) observeNodeSettlementBacklog(parent context.Context, metrics nodeSettlementMetrics) error {
	st, ok := s.Host.Store().(state.ObjectNodeSettlementStore)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, api.ObjectUploadSettlementTimeout)
	defer cancel()
	stats, err := st.ObjectNodeSettlementBacklog(ctx)
	if err != nil {
		return err
	}
	metrics.backlog.WithLabelValues("waiting").Set(float64(stats.Waiting))
	metrics.backlog.WithLabelValues("ready").Set(float64(stats.Ready))
	age := 0.0
	if !stats.OldestReadyAt.IsZero() {
		age = max(0, time.Since(stats.OldestReadyAt).Seconds())
	}
	metrics.oldest.Set(age)
	return nil
}
