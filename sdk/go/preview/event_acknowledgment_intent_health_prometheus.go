package preview

import (
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EventAcknowledgmentIntentHealthPrometheusContentType identifies text 0.0.4.
const EventAcknowledgmentIntentHealthPrometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// EventAcknowledgmentIntentHealthPrometheusOptions controls the fixed metric prefix.
type EventAcknowledgmentIntentHealthPrometheusOptions struct {
	Prefix string // Empty uses gregale_event_acknowledgment_recovery; otherwise ASCII identifier, 1..128 bytes.
}

func eventIntentHealthMetricsPrefix(options EventAcknowledgmentIntentHealthPrometheusOptions) (string, error) {
	prefix := options.Prefix
	if prefix == "" {
		prefix = "gregale_event_acknowledgment_recovery"
	}
	if len(prefix) > 128 {
		return "", ErrInvalidEventConsumerRequest
	}
	for i, c := range []byte(prefix) {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return "", ErrInvalidEventConsumerRequest
		}
	}
	return prefix, nil
}

func eventIntentHealthMetricsAgeValid(age float64) bool {
	return !math.IsNaN(age) && !math.IsInf(age, 0) && age >= 0 && age <= 9007199254740991
}

// FormatEventAcknowledgmentIntentHealthMetrics is pure text 0.0.4 exposition.
// Fixed gauges contain no generated labels, identities or sample timestamps.
// Missing reads omit all sample gauges. Format a fresh snapshot on each scrape.
// Invalid options/snapshots return no text and a fixed error.
func FormatEventAcknowledgmentIntentHealthMetrics(snapshot EventAcknowledgmentIntentHealthMetricsSnapshot, options EventAcknowledgmentIntentHealthPrometheusOptions) (string, error) {
	prefix, err := eventIntentHealthMetricsPrefix(options)
	if err != nil || !eventIntentHealthMetricsAgeValid(snapshot.MaxObservationAgeSeconds) || snapshot.MaxObservationAgeSeconds < 0.1 || snapshot.MaxObservationAgeSeconds > 300 || snapshot.ReadSuccess != (snapshot.Health != nil) {
		return "", ErrInvalidEventConsumerRequest
	}
	var health EventAcknowledgmentIntentHealthMetricsSample
	if snapshot.Health != nil {
		health = *snapshot.Health
		if !eventIntentHealthMetricsSampleValid(health) || !eventIntentHealthMetricsAgeValid(health.ObservationAgeSeconds) {
			return "", ErrInvalidEventConsumerRequest
		}
	}
	if snapshot.EvidenceEligible && (!snapshot.ReadSuccess || !health.Complete || health.ObservationAgeSeconds > snapshot.MaxObservationAgeSeconds) {
		return "", ErrInvalidEventConsumerRequest
	}
	var out strings.Builder
	gauge := func(suffix, help, value string) {
		name := prefix + "_" + suffix
		out.WriteString("# HELP " + name + " " + help + "\n# TYPE " + name + " gauge\n" + name + " " + value + "\n")
	}
	flag := func(value bool) string {
		if value {
			return "1"
		}
		return "0"
	}
	seconds := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
	gauge("read_success", "Latest read supplied valid scoped metadata; zero before a read or after failure.", flag(snapshot.ReadSuccess))
	gauge("evidence_eligible", "Complete fresh advancing evidence remains fresh at snapshot time.", flag(snapshot.EvidenceEligible))
	gauge("max_observation_age_seconds", "Configured maximum observation age in seconds.", seconds(snapshot.MaxObservationAgeSeconds))
	if snapshot.Health != nil {
		gauge("complete", "Latest bounded sample has complete coverage.", flag(health.Complete))
		gauge("truncated", "Latest sample exceeded the scan limit; counts are lower bounds.", flag(health.Truncated))
		gauge("scan_limit", "Maximum sampled unfinished intents.", strconv.Itoa(health.ScanLimit))
		gauge("invalid_rows", "Sampled rows with invalid timestamp metadata.", strconv.FormatInt(health.InvalidRows, 10))
		gauge("outstanding", "Sampled retained unfinished intents including parked work.", strconv.FormatInt(health.Outstanding, 10))
		gauge("due", "Sampled due intents excluding live claims and parked work.", strconv.FormatInt(health.Due, 10))
		gauge("stalled", "Sampled stalled eligible intents excluding parked work.", strconv.FormatInt(health.Stalled, 10))
		gauge("parked", "Sampled retained intents whose recovery claim budget is exhausted.", strconv.FormatInt(health.Parked, 10))
		gauge("observation_timestamp_seconds", "Database observation time in Unix seconds.", eventIntentHealthMetricsTimestamp(health.ObservedAt))
		gauge("observation_age_seconds", "Conservative observation age using wall and local monotonic elapsed time.", seconds(health.ObservationAgeSeconds))
	}
	return out.String(), nil
}

// Preserve decimal subsecond precision without UnixNano overflow outside its
// limited year range, including negative fractional Unix seconds before 1970.
func eventIntentHealthMetricsTimestamp(at time.Time) string {
	whole, fraction := at.Unix(), at.Nanosecond()
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	sign := ""
	if whole < 0 {
		sign, whole, fraction = "-", -whole-1, 1000000000-fraction
	}
	digits := strconv.Itoa(fraction + 1000000000)[1:]
	return sign + strconv.FormatInt(whole, 10) + "." + strings.TrimRight(digits, "0")
}

// NewEventAcknowledgmentIntentHealthMetricsHandler constructs a cache-only
// net/http adapter. It accepts GET /metrics without a query; the host owns the
// listener, authorization/network protection, HTTP deadlines and shutdown.
func NewEventAcknowledgmentIntentHealthMetricsHandler(metrics *EventAcknowledgmentIntentHealthMetrics, options EventAcknowledgmentIntentHealthPrometheusOptions) (http.Handler, error) {
	if !metrics.initialized() {
		return nil, ErrInvalidEventConsumerRequest
	}
	prefix, err := eventIntentHealthMetricsPrefix(options)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.EscapedPath() != "/metrics" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := FormatEventAcknowledgmentIntentHealthMetrics(metrics.Snapshot(), EventAcknowledgmentIntentHealthPrometheusOptions{Prefix: prefix})
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", EventAcknowledgmentIntentHealthPrometheusContentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}), nil
}
