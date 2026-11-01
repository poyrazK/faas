package object

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunObjectStorageUsageExports keeps every usage-driver backend's provider
// report fresh: immediately, then every objectstorage.DefaultUsageExportInterval.
// RunObjectStorageAccounting imports the same files each minute. apid runs the
// exports, not s3-gatewayd, so the provider account keys and the access-log
// bucket stay with the bucket-admin credential on the control plane, and no
// gateway is needed for accounting (ADR-20261031122757). getenv supplies the
// provider account keys the registry names.
func (s *Service) RunObjectStorageUsageExports(ctx context.Context, getenv func(string) string) {
	registry := s.Host.ObjectStorage()
	store, ok := s.Host.Store().(state.ObjectStorageProviderUsageStore)
	if registry == nil || !ok {
		return
	}
	var backends []string
	for _, backend := range registry.Backends() {
		if backend.Usage.Driver != "" {
			backends = append(backends, backend.ID)
		}
	}
	if len(backends) == 0 {
		return
	}
	runs := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_usage_exports_total", Help: "Object-storage provider usage export attempts, by backend and outcome."}, []string{"backend", "outcome"})
	lastSuccess := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_usage_export_last_success_timestamp_seconds", Help: "Unix timestamp of the last successful provider usage export; 0 until the first."}, []string{"backend"})
	if s.Host.Ops() != nil {
		s.Host.Ops().Registry().MustRegister(runs, lastSuccess)
	}
	// Start every backend at 0, so one that never exports is stale at once
	// and FaasObjectStorageUsageExportStale fires for it.
	for _, backend := range backends {
		lastSuccess.WithLabelValues(backend)
	}
	jobs, err := objectstorage.UsageExportJobs(registry, store, getenv)
	if err != nil {
		s.Host.Logger().Error("object storage usage exports not configured", "err", err)
	}
	for _, job := range jobs {
		if err := os.MkdirAll(filepath.Dir(job.ReportPath), 0o700); err != nil {
			s.Host.Logger().Error("object storage usage report directory", "backend_id", job.BackendID, "err", err)
		}
	}
	if len(jobs) == 0 {
		return
	}
	objectstorage.RunUsageExports(ctx, func() []objectstorage.UsageExportJob { return jobs }, objectstorage.DefaultUsageExportInterval, time.Now, func(backend, outcome string, err error) {
		runs.WithLabelValues(backend, outcome).Inc()
		if outcome == "success" {
			lastSuccess.WithLabelValues(backend).Set(float64(time.Now().Unix()))
			return
		}
		s.Host.Logger().Warn("object storage usage export failed", "backend_id", backend, "err", err)
	})
}
