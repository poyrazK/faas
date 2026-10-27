package routeinsights

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics counts evaluator runs for operators (ADR-20261024154324). Its
// methods are nil-receiver safe.
type Metrics struct {
	registry    *prometheus.Registry
	runs        *prometheus.CounterVec
	apps        *prometheus.CounterVec
	resolved    prometheus.Counter
	lastSuccess prometheus.Gauge
}

// NewMetrics registers the evaluator metrics on registry.
func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	m := &Metrics{registry: registry}
	m.runs = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_route_insights_runs_total",
		Help: "Route insights evaluation runs by outcome: success or failed (a candidate read, claim or stale resolve failed).",
	}, []string{"outcome"}))
	m.apps = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_route_insights_apps_total",
		Help: "Apps the route insights evaluator handled by outcome: evaluated, failed, skipped (plan without request telemetry) or taken (another replica claimed it first).",
	}, []string{"outcome"}))
	m.resolved = register(registry, prometheus.NewCounter(prometheus.CounterOpts{
		Name: prefix + "_route_insights_resolved_total",
		Help: "Active route insights resolved because no evaluation observed them within RouteInsightResolveAfter.",
	}))
	m.lastSuccess = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_route_insights_last_success_timestamp_seconds",
		Help: "Unix time this replica last finished a route insights evaluation run without error; zero until one has.",
	}))
	for _, outcome := range []string{"success", "failed"} {
		m.runs.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"evaluated", "failed", "skipped", "taken"} {
		m.apps.WithLabelValues(outcome)
	}
	return m
}

func (m *Metrics) observeRun(stats Stats, err error, at time.Time) {
	if m == nil {
		return
	}
	m.apps.WithLabelValues("evaluated").Add(float64(stats.Apps - stats.Failed - stats.Skipped))
	m.apps.WithLabelValues("failed").Add(float64(stats.Failed))
	m.apps.WithLabelValues("skipped").Add(float64(stats.Skipped))
	m.apps.WithLabelValues("taken").Add(float64(stats.Taken))
	m.resolved.Add(float64(stats.Resolved))
	if err != nil {
		m.runs.WithLabelValues("failed").Inc()
		return
	}
	m.runs.WithLabelValues("success").Inc()
	m.lastSuccess.Set(float64(at.Unix()))
}

func register[T prometheus.Collector](registry *prometheus.Registry, c T) T {
	err := registry.Register(c)
	if err == nil {
		return c
	}
	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(T); ok {
			return existing
		}
	}
	panic(err)
}
