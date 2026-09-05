// metrics.go — Prometheus counters for the log archive shipper
// (issue #562). Mirrors the §12 naming convention used by every
// other ops metric in the platform: the daemon prefix owns the
// shipper (normally "apid_" or "vmmd_"), counters are suffixed _total, gauges
// carry the suffix _bytes / _seconds / etc.
//
// The metric registration follows the single-registry pattern
// (memory wire-opsmetrics-single-registry): every counter is
// constructed with a fresh prometheus.NewCounter (NOT a
// CounterVec with high-cardinality labels). The shipper's
// reason labels are a closed set {network, auth, throttle, size,
// spool_full, spool_write, queue_full, body_length} so the closed-set pattern
// is safe.
//
// The daemon owns the registry; the shipper holds a
// pointer to OpsMetrics and increments through it. The Metrics
// type defined here is the narrow surface the shipper needs
// — pkg/wire.OpsMetrics implements it transparently via the
// methods exposed below.

package logarchive

import "github.com/prometheus/client_golang/prometheus"

// Metric names follow §12 conventions: every name is suffixed
// with the unit (_total / _bytes / _seconds), and the prefix
// matches the apid daemon since apid owns the shipper.
const (
	metricFilesUploadedTotal    = "apid_log_archive_files_uploaded_total"
	metricBytesUploadedTotal    = "apid_log_archive_bytes_uploaded_total"
	metricFailuresTotal         = "apid_log_archive_failures_total"
	metricLocalBytes            = "apid_log_archive_local_bytes"
	metricLocalBytesMax         = "apid_log_archive_local_bytes_max"
	metricFlushDurationSeconds  = "apid_log_archive_flush_duration_seconds"
	metricUploadDurationSeconds = "apid_log_archive_upload_duration_seconds"
)

// Metrics is the narrow surface the shipper reads from
// pkg/wire.OpsMetrics. The production code constructs it via
// NewMetricsWithPrefix; tests can construct a
// *recordingMetrics to inspect increments without a real
// registry.
type Metrics interface {
	IncFilesUploaded(status string)
	AddBytesUploaded(n int64)
	IncFailure(reason string)
	SetLocalBytes(n int64)
	ObserveFlushDuration(seconds float64)
	ObserveUploadDuration(seconds float64)
}

// Failure reason closed set (pre-instantiated in NewMetrics so
// the rows surface in /metrics from boot). Adding a new reason
// requires extending this list + the switch in IncFailure.
const (
	FailureReasonNetwork    = "network"
	FailureReasonAuth       = "auth"
	FailureReasonThrottle   = "throttle"
	FailureReasonSize       = "size"
	FailureReasonSpoolFull  = "spool_full"
	FailureReasonSpoolWrite = "spool_write"
	FailureReasonQueueFull  = "queue_full"
	FailureReasonBodyLength = "body_length"
	FailureReasonOther      = "other"
)

// promMetrics is the production Metrics implementation backed by
// a fresh prometheus.Registry (single-registry pattern, memory
// wire-opsmetrics-single-registry). The constructor attaches
// the counters to the supplied registry so the daemon's
// /metrics scrape surfaces them.
type promMetrics struct {
	filesUploaded  *prometheus.CounterVec
	bytesUploaded  prometheus.Counter
	failures       *prometheus.CounterVec
	localBytes     prometheus.Gauge
	localBytesMax  prometheus.Gauge
	flushDuration  prometheus.Histogram
	uploadDuration prometheus.Histogram
}

// NewMetrics constructs the Prometheus-backed Metrics using the
// historical apid_ prefix. Keep this wrapper so existing apid
// callers and dashboards remain source-compatible.
func NewMetrics(reg prometheus.Registerer) Metrics {
	return NewMetricsWithPrefix(reg, "apid")
}

// NewMetricsWithPrefix constructs the Prometheus-backed Metrics
// under prefix. Each daemon owns its registry, so vmmd can expose
// vmmd_log_archive_* without colliding with apid's shipper series.
// Nil registry returns a no-op Metrics so tests can skip the registry.
func NewMetricsWithPrefix(reg prometheus.Registerer, prefix string) Metrics {
	if reg == nil {
		return noopMetrics{}
	}
	if prefix == "" {
		prefix = "apid"
	}
	files := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_log_archive_files_uploaded_total",
		Help: "Files the log archive shipper uploaded to S3 (issue #562). status ∈ {ok, err}. ok increments once per successful gzip+PUT; err increments once per failed upload before the file is left for retry.",
	}, []string{"status"})
	bytes := prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_log_archive_bytes_uploaded_total",
		Help: "Compressed bytes the log archive shipper uploaded to S3 (issue #562).",
	})
	failures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_log_archive_failures_total",
		Help: "Log archive failure counter (issue #562). reason ∈ {network, auth, throttle, size, spool_full, spool_write, queue_full, body_length, other}.",
	}, []string{"reason"})
	localBytes := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_log_archive_local_bytes",
		Help: "Current unshipped local log archive bytes (issue #562).",
	})
	localBytesMax := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_log_archive_local_bytes_max",
		Help: "Configured local log archive capacity in bytes (issue #562).",
	})
	flushDur := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    prefix + "_log_archive_flush_duration_seconds",
		Help:    "Wall-clock time for a single log archive shipper tick (issue #562).",
		Buckets: []float64{0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
	})
	uploadDur := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    prefix + "_log_archive_upload_duration_seconds",
		Help:    "Wall-clock time for a single log archive PutObject (issue #562).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})
	reg.MustRegister(files, bytes, failures, localBytes, localBytesMax, flushDur, uploadDur)
	for _, status := range []string{"ok", "err"} {
		files.WithLabelValues(status)
	}
	for _, reason := range []string{FailureReasonNetwork, FailureReasonAuth, FailureReasonThrottle, FailureReasonSize, FailureReasonSpoolFull, FailureReasonSpoolWrite, FailureReasonQueueFull, FailureReasonBodyLength, FailureReasonOther} {
		failures.WithLabelValues(reason)
	}
	return &promMetrics{
		filesUploaded:  files,
		bytesUploaded:  bytes,
		failures:       failures,
		localBytes:     localBytes,
		localBytesMax:  localBytesMax,
		flushDuration:  flushDur,
		uploadDuration: uploadDur,
	}
}

func (p *promMetrics) IncFilesUploaded(status string) {
	if p == nil {
		return
	}
	switch status {
	case "ok", "err":
		p.filesUploaded.WithLabelValues(status).Inc()
	}
}

func (p *promMetrics) AddBytesUploaded(n int64) {
	if p == nil {
		return
	}
	p.bytesUploaded.Add(float64(n))
}

func (p *promMetrics) IncFailure(reason string) {
	if p == nil {
		return
	}
	switch reason {
	case FailureReasonNetwork, FailureReasonAuth, FailureReasonThrottle, FailureReasonSize, FailureReasonSpoolFull, FailureReasonSpoolWrite, FailureReasonQueueFull, FailureReasonBodyLength, FailureReasonOther:
		p.failures.WithLabelValues(reason).Inc()
	}
}

func (p *promMetrics) SetLocalBytes(n int64) {
	if p == nil {
		return
	}
	p.localBytes.Set(float64(n))
}

// SetLocalBytesMax publishes the effective local spool capacity. It is kept
// out of the Metrics interface so existing test fakes remain source
// compatible; the Shipper uses the optional capability when present.
func (p *promMetrics) SetLocalBytesMax(n int64) {
	if p == nil {
		return
	}
	p.localBytesMax.Set(float64(n))
}

func (p *promMetrics) ObserveFlushDuration(seconds float64) {
	if p == nil {
		return
	}
	p.flushDuration.Observe(seconds)
}

func (p *promMetrics) ObserveUploadDuration(seconds float64) {
	if p == nil {
		return
	}
	p.uploadDuration.Observe(seconds)
}

// noopMetrics is the nil-registry fallback used in tests that
// don't wire a registry.
type noopMetrics struct{}

func (noopMetrics) IncFilesUploaded(string)       {}
func (noopMetrics) AddBytesUploaded(int64)        {}
func (noopMetrics) IncFailure(string)             {}
func (noopMetrics) SetLocalBytes(int64)           {}
func (noopMetrics) ObserveFlushDuration(float64)  {}
func (noopMetrics) ObserveUploadDuration(float64) {}

// recordingMetrics captures increments for tests. Sibling of
// noopMetrics; the test asserts the counter deltas after a
// flush / purge cycle.
type recordingMetrics struct {
	filesOK           int
	filesErr          int
	bytes             int64
	failures          map[string]int
	localBytesUpdates []int64
	flushDurations    []float64
	uploadDurations   []float64
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{failures: make(map[string]int)}
}

func (r *recordingMetrics) IncFilesUploaded(status string) {
	switch status {
	case "ok":
		r.filesOK++
	case "err":
		r.filesErr++
	}
}

func (r *recordingMetrics) AddBytesUploaded(n int64) { r.bytes += n }

func (r *recordingMetrics) IncFailure(reason string) { r.failures[reason]++ }

func (r *recordingMetrics) SetLocalBytes(n int64) {
	r.localBytesUpdates = append(r.localBytesUpdates, n)
}

func (r *recordingMetrics) ObserveFlushDuration(s float64) {
	r.flushDurations = append(r.flushDurations, s)
}

func (r *recordingMetrics) ObserveUploadDuration(s float64) {
	r.uploadDurations = append(r.uploadDurations, s)
}
