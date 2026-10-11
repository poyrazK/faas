package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func (s *server) runAppHealthCollector(ctx context.Context) {
	store, ok := s.store.(apphealth.CollectorStore)
	if !ok {
		s.log.Error("app health collection store unavailable")
		return
	}
	collector := apphealth.Collector{Store: store, Client: s.promqlClient, Logger: s.log}
	if s.ops != nil {
		factory := promauto.With(s.ops.Registry())
		checks := factory.NewCounterVec(prometheus.CounterOpts{Name: "app_health_collection_total", Help: "Background app health observations by completion outcome."}, []string{"outcome"})
		duration := factory.NewHistogram(prometheus.HistogramOpts{Name: "app_health_collection_duration_seconds", Help: "Time spent collecting and persisting app health evidence.", Buckets: prometheus.DefBuckets})
		collector.Observe = func(outcome string, elapsed time.Duration) {
			checks.WithLabelValues(outcome).Inc()
			duration.Observe(elapsed.Seconds())
		}
	}
	if err := collector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("app health collector exited", "error", err)
	}
}
