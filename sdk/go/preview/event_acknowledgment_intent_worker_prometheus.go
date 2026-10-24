package faas

import (
	"io"
	"net/http"
	"strconv"
	"strings"
)

// EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions selects a fixed
// identifier prefix. Empty uses gregale_event_acknowledgment_recovery_worker.
type EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions struct {
	Prefix string
}

func eventIntentRecoveryWorkerSnapshotValid(s EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot) bool {
	if !eventIntentHealthMetricsAgeValid(s.MaxIdleSeconds) || s.MaxIdleSeconds < 0.1 || s.MaxIdleSeconds > 86400 ||
		!eventIntentHealthMetricsAgeValid(s.MaxPassSeconds) || s.MaxPassSeconds < 1 || s.MaxPassSeconds > 86400 ||
		!eventIntentCheckpointTime(s.ActivityAt) || !eventIntentHealthMetricsAgeValid(s.ActivityAgeSeconds) {
		return false
	}
	bound := s.MaxIdleSeconds
	if s.InFlight {
		bound = s.MaxPassSeconds
	}
	if s.Overdue != (s.ClockValid && s.ActivityAgeSeconds > bound) {
		return false
	}
	if s.LastStartedAt == nil {
		return !s.InFlight && s.LastPass == nil
	}
	if !eventIntentCheckpointTime(*s.LastStartedAt) || s.InFlight && !s.ActivityAt.Equal(*s.LastStartedAt) {
		return false
	}
	if s.LastPass == nil {
		return s.InFlight
	}
	p := s.LastPass
	if !eventIntentCheckpointTime(p.StartedAt) || !eventIntentCheckpointTime(p.CompletedAt) ||
		!eventIntentHealthMetricsAgeValid(p.DurationSeconds) || !eventIntentHealthMetricsAgeValid(p.CompletionAgeSeconds) ||
		p.Result != nil && !eventIntentRecoveryWorkerResultValid(*p.Result) {
		return false
	}
	if !s.InFlight && (!s.ActivityAt.Equal(p.CompletedAt) || !s.LastStartedAt.Equal(p.StartedAt) || s.ActivityAgeSeconds != p.CompletionAgeSeconds) {
		return false
	}
	return !s.ClockValid || !p.CompletedAt.Before(p.StartedAt) &&
		(!s.InFlight || !s.LastStartedAt.Before(p.CompletedAt))
}

// FormatEventAcknowledgmentIntentRecoveryWorkerMetrics renders pure text 0.0.4.
// Gauges contain only bounded local activity/progress, no identity/error labels,
// no explicit sample timestamps and no recovery authority. Missing pass results
// omit outcome gauges. Use the health Prometheus content type for HTTP responses.
func FormatEventAcknowledgmentIntentRecoveryWorkerMetrics(snapshot EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot, options EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions) (string, error) {
	if options.Prefix == "" {
		options.Prefix = "gregale_event_acknowledgment_recovery_worker"
	}
	prefix, err := eventIntentHealthMetricsPrefix(EventAcknowledgmentIntentHealthPrometheusOptions{Prefix: options.Prefix})
	// Copy caller-owned nested metadata before validation/formatting. Callers must
	// not mutate their snapshot concurrently; no internal cache aliases escape.
	snapshot.LastStartedAt = cloneEventIntentTime(snapshot.LastStartedAt)
	if snapshot.LastPass != nil {
		copy := *snapshot.LastPass
		if copy.Result != nil {
			result := *copy.Result
			copy.Result = &result
		}
		snapshot.LastPass = &copy
	}
	if err != nil || !eventIntentRecoveryWorkerSnapshotValid(snapshot) {
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
	gauge("clock_valid", "Local activity clock has no detected regression since monitoring began.", flag(snapshot.ClockValid))
	gauge("overdue", "Valid local activity age exceeds the idle or in-flight pass bound.", flag(snapshot.Overdue))
	gauge("in_flight", "One explicitly monitored recovery pass has started and has not returned.", flag(snapshot.InFlight))
	gauge("max_idle_seconds", "Maximum expected gap before the first or next recovery pass starts.", seconds(snapshot.MaxIdleSeconds))
	gauge("max_pass_seconds", "Maximum expected duration of a monitored recovery pass; not a deadline.", seconds(snapshot.MaxPassSeconds))
	gauge("activity_timestamp_seconds", "Latest actual pass start or joined return, initially monitoring construction time.", eventIntentHealthMetricsTimestamp(snapshot.ActivityAt))
	gauge("activity_age_seconds", "Conservative age of actual activity using wall and local monotonic elapsed time.", seconds(snapshot.ActivityAgeSeconds))
	gauge("last_pass_result_valid", "Latest joined pass has valid bounded scalar progress; zero before any return.", flag(snapshot.LastPass != nil && snapshot.LastPass.Result != nil))
	if snapshot.LastStartedAt != nil {
		gauge("last_started_timestamp_seconds", "Latest actual monitored recovery pass start in Unix seconds.", eventIntentHealthMetricsTimestamp(*snapshot.LastStartedAt))
	}
	if p := snapshot.LastPass; p != nil {
		gauge("last_completed_timestamp_seconds", "Latest monitored recovery pass joined return in Unix seconds, regardless of outcome.", eventIntentHealthMetricsTimestamp(p.CompletedAt))
		gauge("last_pass_duration_seconds", "Monotonic elapsed duration of the latest joined recovery pass.", seconds(p.DurationSeconds))
		gauge("last_completed_age_seconds", "Conservative age of the latest joined recovery pass return.", seconds(p.CompletionAgeSeconds))
		if r := p.Result; r != nil {
			gauge("last_pass_success", "Latest pass stopped at its work limit or an empty probe; not callback or business success.", flag(r.StopReason == "limit" || r.StopReason == "empty"))
			gauge("last_pass_attempts", "Admissions in the latest joined pass, including empty probes.", strconv.Itoa(r.Attempts))
			gauge("last_pass_confirmed", "Confirmed settlements in the latest joined pass.", strconv.Itoa(r.Confirmed))
			gauge("last_pass_unconfirmed", "Known unconfirmed settlements in the latest joined pass.", strconv.Itoa(r.Unconfirmed))
			gauge("last_pass_parked", "Known parking outcomes in the latest joined pass; not total retained parked work.", strconv.Itoa(r.Parked))
			gauge("last_pass_superseded", "Superseded outcomes in the latest joined pass.", strconv.Itoa(r.Superseded))
			gauge("last_pass_empty", "Empty probes in the latest joined pass; not proof of global drain.", strconv.Itoa(r.Empty))
			gauge("last_pass_claim_lost", "Lost recovery claims in the latest joined pass.", strconv.Itoa(r.ClaimLost))
			gauge("last_pass_interrupted", "Interrupted admissions in the latest joined pass.", strconv.Itoa(r.Interrupted))
			gauge("last_pass_failed", "Failed admissions in the latest joined pass, excluding unknown commits.", strconv.Itoa(r.Failed))
			gauge("last_pass_commit_unknown", "Admissions with unknown commit outcome in the latest joined pass.", strconv.Itoa(r.CommitUnknown))
		}
	}
	return out.String(), nil
}

// NewEventAcknowledgmentIntentRecoveryMetricsHandler combines matching health
// and worker caches under their fixed default prefixes. It performs no I/O except
// HTTP writes and starts no pass. The host owns listener protection and lifetime.
func NewEventAcknowledgmentIntentRecoveryMetricsHandler(health *EventAcknowledgmentIntentHealthMetrics, worker *EventAcknowledgmentIntentRecoveryWorkerMetrics) (http.Handler, error) {
	if !health.initialized() || !worker.initialized() || health.account != worker.account || health.app != worker.app {
		return nil, ErrInvalidEventConsumerRequest
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
		healthBody, healthErr := FormatEventAcknowledgmentIntentHealthMetrics(health.Snapshot(), EventAcknowledgmentIntentHealthPrometheusOptions{})
		workerBody, workerErr := FormatEventAcknowledgmentIntentRecoveryWorkerMetrics(worker.Snapshot(), EventAcknowledgmentIntentRecoveryWorkerPrometheusOptions{})
		if healthErr != nil || workerErr != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		body := healthBody + workerBody
		w.Header().Set("Content-Type", EventAcknowledgmentIntentHealthPrometheusContentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}), nil
}
