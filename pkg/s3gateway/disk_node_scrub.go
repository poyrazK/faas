package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/prometheus/client_golang/prometheus"
)

func (h *DiskHandler) scrub(w http.ResponseWriter, r *http.Request, bucket, payload, id string) {
	w.Header().Set("Cache-Control", "no-store")
	q := operationQuery(r.URL.Query())
	if bucket != "" || r.ContentLength != 0 || !diskMultipartQuery(q, objectinventory.Query, "cursor", "max-keys") || r.Header.Values(objectstorage.OwnedNodeReceiptHeader) != nil || r.Header.Values(objectstorage.OwnedNodeMultipartReceiptHeader) != nil || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ObjectCapacityInventoryTimeout)
	defer cancel()
	if q.Get(objectinventory.Query) == objectinventory.ScrubFindings && r.Method == http.MethodGet {
		limit, err := listLimit(q, "max-keys")
		if err != nil {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		page, err := h.config.Store.ScrubFindings(ctx, q.Get("cursor"), limit)
		h.inventoryResult(w, r, id, page, err)
		return
	}
	if q.Get(objectinventory.Query) != objectinventory.ScrubKind || q.Has("cursor") || q.Has("max-keys") {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := h.config.Store.ScrubStatus()
		h.inventoryResult(w, r, id, status, err)
	case http.MethodPost:
		if _, err := readVerifiedRequestBody(w, r, payload, 0); err != nil {
			h.failure(w, r, id, objectnode.ErrInvalid)
			return
		}
		status, err := h.startScrub(ctx)
		h.inventoryResult(w, r, id, status, err)
	default:
		h.failure(w, r, id, objectnode.ErrInvalid)
	}
}

func (h *DiskHandler) startScrub(ctx context.Context) (objectnode.ScrubStatus, error) {
	if !h.reserve(api.ObjectNodeScrubManifestMaxBytes) {
		return objectnode.ScrubStatus{}, objectnode.ErrCapacity
	}
	defer h.release(api.ObjectNodeScrubManifestMaxBytes)
	return h.config.Store.StartScrub(ctx)
}

type diskScrubMetrics struct {
	steps      *prometheus.CounterVec
	targets    *prometheus.GaugeVec
	bytes      *prometheus.GaugeVec
	timestamps *prometheus.GaugeVec
	running    prometheus.Gauge
	workerOK   prometheus.Gauge
}

func newDiskScrubMetrics(registry *prometheus.Registry) *diskScrubMetrics {
	m := &diskScrubMetrics{
		steps:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_node_scrub_steps_total", Help: "Background checksum scrub steps by outcome."}, []string{"outcome"}),
		targets:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_node_scrub_targets", Help: "Durable counts from the current or most recent pinned manifest; not current-object corruption counts."}, []string{"outcome"}),
		bytes:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_node_scrub_bytes", Help: "Durable blob read progress for the current or most recent checksum scan."}, []string{"kind"}),
		timestamps: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_node_scrub_timestamp_seconds", Help: "Durable checksum scan timestamps; verified_manifest means every pinned identity was verified without failures or stale targets."}, []string{"event"}),
		running:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_scrub_running", Help: "Whether a durable checksum worklist is in progress."}),
		workerOK:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_scrub_worker_ok", Help: "Whether the last background scrub step completed without an operational error; zero before its first step."}),
	}
	registry.MustRegister(m.steps, m.targets, m.bytes, m.timestamps, m.running, m.workerOK)
	return m
}

func (h *DiskHandler) observeScrub(status objectnode.ScrubStatus) {
	for name, value := range map[string]int64{"total": status.Targets, "processed": status.Processed, "verified": status.Verified, "failed": status.Failures, "stale": status.Stale} {
		h.metrics.scrub.targets.WithLabelValues(name).Set(float64(value))
	}
	h.metrics.scrub.bytes.WithLabelValues("read").Set(float64(status.BytesRead))
	h.metrics.scrub.bytes.WithLabelValues("current_offset").Set(float64(status.CurrentOffset))
	for event, when := range map[string]time.Time{"completed": status.LastCompletedAt, "verified_manifest": status.LastVerifiedAt, "progress": status.UpdatedAt} {
		h.metrics.scrub.timestamps.WithLabelValues(event).Set(scrubTimestamp(when))
	}
	running := 0.0
	if status.State == "running" {
		running = 1
	}
	h.metrics.scrub.running.Set(running)
}

func scrubTimestamp(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

// A single worker dispatches one read budget per tick. Start operations also
// reserve spool space through the node's ordinary write admission.
func (h *DiskHandler) RunScrubber(ctx context.Context, bytesPerStep int64, interval time.Duration, observe func(error)) {
	if errors.Is(h.config.Store.CanServe(), objectnode.ErrFenced) {
		return
	}
	ticker := time.NewTicker(api.ObjectNodeScrubStepInterval)
	defer ticker.Stop()
	var retryAt time.Time
	run := func() {
		if time.Now().Before(retryAt) {
			return
		}
		scan, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
		defer cancel()
		status, err := h.config.Store.ScrubStatus()
		if err == nil && status.State != "running" && (status.LastCompletedAt.IsZero() || time.Since(status.LastCompletedAt) >= interval) {
			status, err = h.startScrub(scan)
		}
		if err == nil && status.State == "running" {
			status, err = h.config.Store.ScrubStep(scan, bytesPerStep)
		}
		if err != nil {
			h.metrics.scrub.workerOK.Set(0)
			h.metrics.scrub.steps.WithLabelValues("failed").Inc()
			retryAt = time.Now().Add(api.ObjectNodeScrubRetry)
			if observe != nil && ctx.Err() == nil {
				observe(err)
			}
			return
		}
		h.observeScrub(status)
		h.metrics.scrub.workerOK.Set(1)
		h.metrics.scrub.steps.WithLabelValues(status.State).Inc()
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if errors.Is(h.config.Store.CanServe(), objectnode.ErrFenced) {
				return
			}
			run()
		}
	}
}
