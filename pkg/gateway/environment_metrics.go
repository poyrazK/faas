package gateway

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// Requests served on an environment host (a stage URL or an environment's
// scoped domain) are also counted per environment, so a stage's alert rules
// read the stage's own traffic (ADR-20261104120000 amendment 2026-11-22).
// The app-wide series stay as they are; these add one series set per
// environment host an app serves.

func newEnvironmentRequestDuration() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gateway_environment_request_duration_seconds",
		Help: "Full request duration of requests served on an environment host, labelled by app, environment ID and HTTP status class (2xx/3xx/4xx/5xx). The same requests are also in gateway_request_duration_seconds.",
		Buckets: []float64{
			0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, api.RequestDurationTopBucketSeconds,
		},
	}, []string{"app", "environment", "class"})
}

func newEnvironmentColdBoot() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_environment_cold_boot_total",
		Help: "Cold boots triggered by requests on an environment host, labelled by app and environment ID. The same boots are also in gateway_cold_boot_total.",
	}, []string{"app", "environment"})
}

func newEnvironmentCapacityRefusals() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_environment_capacity_refusals_total",
		Help: "Capacity refusals (see gateway_capacity_refusals_total) of requests on an environment host, labelled by app and environment ID.",
	}, []string{"app", "environment"})
}

// ObserveEnvironmentCapacityRefusal records a capacity refusal on an
// environment host. An empty environment records nothing.
func (m *Metrics) ObserveEnvironmentCapacityRefusal(appID, environmentID string) {
	if m == nil || m.environmentCapacityRefusals == nil || appID == "" || environmentID == "" {
		return
	}
	m.environmentCapacityRefusals.WithLabelValues(appID, environmentID).Inc()
}

// ObserveEnvironmentRequestDuration records a request served on an
// environment host. An empty environment records nothing.
func (m *Metrics) ObserveEnvironmentRequestDuration(appID, environmentID, class string, d time.Duration) {
	if m == nil || m.environmentRequestDuration == nil || appID == "" || environmentID == "" {
		return
	}
	m.environmentRequestDuration.WithLabelValues(appID, environmentID, class).Observe(d.Seconds())
}

// ObserveEnvironmentColdBoot records a cold boot on an environment host. An
// empty environment records nothing.
func (m *Metrics) ObserveEnvironmentColdBoot(appID, environmentID string) {
	if m == nil || m.environmentColdBoot == nil || appID == "" || environmentID == "" {
		return
	}
	m.environmentColdBoot.WithLabelValues(appID, environmentID).Inc()
}

type environmentHostKey struct{}

// withEnvironmentHostMark gives the request a slot for the environment host
// it resolves to; observe reads it after the response.
func withEnvironmentHostMark(ctx context.Context) context.Context {
	return context.WithValue(ctx, environmentHostKey{}, new(atomic.Pointer[string]))
}

// markEnvironmentHost records the environment whose host served r.
func markEnvironmentHost(r *http.Request, environmentID string) {
	if r == nil || environmentID == "" {
		return
	}
	if slot, ok := r.Context().Value(environmentHostKey{}).(*atomic.Pointer[string]); ok && slot != nil {
		slot.Store(&environmentID)
	}
}

// environmentHostFrom returns the environment markEnvironmentHost recorded
// for r, or "".
func environmentHostFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	slot, ok := r.Context().Value(environmentHostKey{}).(*atomic.Pointer[string])
	if !ok || slot == nil {
		return ""
	}
	if id := slot.Load(); id != nil {
		return *id
	}
	return ""
}
