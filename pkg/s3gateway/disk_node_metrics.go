package s3gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type diskMetrics struct {
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	responseBytes prometheus.Counter
	scans         *prometheus.CounterVec
	lastSuccess   prometheus.Gauge
	healthy       prometheus.Gauge
	bytes         *prometheus.GaugeVec
	items         *prometheus.GaugeVec
	scrub         *diskScrubMetrics
	replica       *diskReplicaMetrics
	replication   *diskReplicationWaitMetrics
}

func newDiskMetrics(h *DiskHandler) *diskMetrics {
	m := &diskMetrics{
		registry:      prometheus.NewRegistry(),
		requests:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_node_requests_total", Help: "Node HTTP attempts, including failures; process-local operational counter."}, []string{"method", "status_class"}),
		duration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "faas_object_node_request_duration_seconds", Help: "Node HTTP attempt duration."}, []string{"method"}),
		responseBytes: prometheus.NewCounter(prometheus.CounterOpts{Name: "faas_object_node_response_bytes_total", Help: "Response bytes written by this process; not an authoritative egress meter."}),
		scans:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_node_inventory_scans_total", Help: "Read-only node inventory audits."}, []string{"outcome"}),
		lastSuccess:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_inventory_last_success_timestamp_seconds", Help: "Last complete inventory audit with a healthy catalog; zero until one succeeds."}),
		healthy:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_object_node_catalog_healthy", Help: "Whether the latest complete inventory audit found a healthy catalog."}),
		bytes:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_node_inventory_bytes", Help: "Latest complete inventory byte observations; physical file sizes are sampled."}, []string{"kind"}),
		items:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_node_inventory_items", Help: "Latest complete inventory item counts."}, []string{"kind"}),
	}
	m.registry.MustRegister(m.requests, m.duration, m.responseBytes, m.scans, m.lastSuccess, m.healthy, m.bytes, m.items,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_active_writes", Help: "Active PUT, part and assembly reservations."}, func() float64 { return float64(len(h.writes)) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_reserved_bytes", Help: "Bytes reserved by active writes and assemblies."}, func() float64 { h.mu.Lock(); defer h.mu.Unlock(); return float64(h.reserved) }),
	)
	m.scrub = newDiskScrubMetrics(m.registry)
	m.replica = newDiskReplicaMetrics(h, m.registry)
	m.replication = newDiskReplicationWaitMetrics(m.registry)
	registerRetentionMetrics(h, m.registry)
	registerCompactionMetrics(h, m.registry)
	registerRecoveryCleanupMetrics(h, m.registry)
	registerCatalogIndexMetrics(h, m.registry)
	registerUsageCounterMetrics(h, m.registry)
	registerInventoryAuditMetrics(h, m.registry)
	registerWriteAdmissionMetrics(h, m.registry)
	return m
}

// MetricsHandler serves cached aggregate observations. It performs no disk scan
// and never exposes bucket, key, credential or receipt labels.
func (h *DiskHandler) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(h.metrics.registry, promhttp.HandlerOpts{})
}

func (h *DiskHandler) RefreshInventory(ctx context.Context) (objectinventory.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
	defer cancel()
	u, err := h.config.Store.Usage(ctx)
	if err != nil {
		h.metrics.scans.WithLabelValues("failed").Inc()
		h.metrics.healthy.Set(0)
		return u, err
	}
	h.recordInventory(u, u.FinishedAt)
	return u, nil
}

func (h *DiskHandler) recordInventory(u objectinventory.Usage, healthyAt time.Time) {
	if u.CatalogHealthy {
		h.metrics.lastSuccess.Set(float64(healthyAt.Unix()))
		h.metrics.healthy.Set(1)
		h.metrics.scans.WithLabelValues("success").Inc()
	} else {
		h.metrics.healthy.Set(0)
		h.metrics.scans.WithLabelValues("unhealthy").Inc()
	}
	for name, value := range map[string]int64{"committed": u.CommittedBytes, "multipart": u.MultipartBytes, "receipts": u.ReceiptBytes, "files": u.FileBytes, "allocated": u.AllocatedBytes, "data": u.DataBytes, "staging": u.StagingBytes, "trash": u.TrashBytes, "metadata": u.MetadataBytes, "unreferenced": u.UnreferencedBytes} {
		h.metrics.bytes.WithLabelValues(name).Set(float64(value))
	}
	for name, value := range map[string]int64{"buckets": u.Buckets, "objects": u.Objects, "multipart_uploads": u.MultipartUploads, "multipart_parts": u.MultipartParts, "receipts": u.Receipts, "sessions": u.Sessions, "files": u.Files, "unreferenced_blobs": u.UnreferencedBlobs, "errors": u.Errors, "warnings": u.Warnings} {
		h.metrics.items.WithLabelValues(name).Set(float64(value))
	}
	h.metrics.bytes.WithLabelValues("disk").Set(float64(u.DiskBytes))
	h.metrics.bytes.WithLabelValues("available").Set(float64(u.AvailableBytes))
}

type diskResponse struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *diskResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *diskResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *diskResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (h *DiskHandler) measureRequest(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	start, response := time.Now(), &diskResponse{ResponseWriter: w}
	return response, func() {
		method := r.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost, http.MethodDelete:
		default:
			method = "other"
		}
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		h.metrics.requests.WithLabelValues(method, strconv.Itoa(status/100)+"xx").Inc()
		h.metrics.duration.WithLabelValues(method).Observe(time.Since(start).Seconds())
		h.metrics.responseBytes.Add(float64(response.bytes))
	}
}
