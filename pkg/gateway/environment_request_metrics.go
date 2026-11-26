package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
)

type environmentRequestMetrics struct {
	duration *prometheus.HistogramVec
	refusals *prometheus.CounterVec
	primed   sync.Map
}

var requestDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, api.RequestDurationTopBucketSeconds,
}

func newEnvironmentRequestMetrics() *environmentRequestMetrics {
	return &environmentRequestMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_environment_request_duration_seconds",
			Help:    "HTTP request duration for resolved app environments by response class.",
			Buckets: requestDurationBuckets,
		}, []string{"app", "scope", "class"}),
		refusals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_environment_capacity_refusals_total",
			Help: "Capacity refusals for resolved app environments.",
		}, []string{"app", "scope"}),
	}
}

type requestEnvironmentKey struct{}

type requestEnvironment struct {
	appID, scope string
}

// Resolve ownership from the gateway's verified host binding. Exact revision
// and pull-request preview hostnames do not represent environment traffic.
func withRequestEnvironment(ctx context.Context, app App) context.Context {
	limits, ok := api.LimitsFor(app.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 || app.IsPreview ||
		(app.PinnedDeploymentID != "" && app.PublicEnvironmentID == "") {
		return ctx
	}
	scope := app.Scope
	if app.PinnedDeploymentScope != "" {
		scope = app.PinnedDeploymentScope
	}
	if scope == "" && app.ProjectID == "" {
		scope = api.DefaultEnvScope
	}
	return context.WithValue(ctx, requestEnvironmentKey{}, requestEnvironment{appID: app.ID, scope: scope})
}

func (m *environmentRequestMetrics) observe(ctx context.Context, appID, class string, elapsed time.Duration, refused bool) {
	if m == nil {
		return
	}
	env, ok := ctx.Value(requestEnvironmentKey{}).(requestEnvironment)
	if !ok || env.appID != appID {
		return
	}
	// The verified routing decision tells us which roster won for an
	// unpinned hostname. A pinned environment gets its scope from the host.
	if routing, ok := publicRoutingSnapshot(ctx); ok && routing.AppID == appID &&
		routing.SelectedDeploymentID != "" && routing.HostDeploymentID == "" {
		env.scope = routing.admissionScope()
		if env.scope == "" {
			env.scope = api.DefaultEnvScope
		}
	}
	if env.scope != api.DefaultEnvScope && !api.ValidProjectEnvironmentSlug(env.scope) {
		return
	}
	if _, ok := m.primed.Load(env); !ok {
		for _, class := range []string{"1xx", "2xx", "3xx", "4xx", "5xx"} {
			m.duration.WithLabelValues(appID, env.scope, class)
		}
		m.refusals.WithLabelValues(appID, env.scope)
		m.primed.Store(env, struct{}{})
	}
	m.duration.WithLabelValues(appID, env.scope, class).Observe(elapsed.Seconds())
	if refused {
		m.refusals.WithLabelValues(appID, env.scope).Inc()
	}
}
