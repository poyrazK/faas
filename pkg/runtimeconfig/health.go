package runtimeconfig

import (
	"context"
	"fmt"
	"math"
	"time"
)

// PromQLClient is the small query surface needed by the rollout safety gate.
// It is intentionally local so callers can use the existing apid Prometheus
// client or inject a deterministic test double.
type PromQLClient interface {
	QueryScalar(ctx context.Context, query string) (float64, error)
}

// HealthSnapshot contains fleet-level signals used while a runtime-config
// canary is serving traffic. The metrics are deliberately independent of the
// flag being changed: a bad daemon-level flag should show up as a regression
// in the same request path the operator is protecting.
type HealthSnapshot struct {
	Requests     float64
	ErrorRatePct float64
	P95LatencyMs float64
	ObservedAt   time.Time
}

// HealthPolicy defines the minimum evidence and maximum tolerated regression
// for a canary. A zero value is not useful in production, so callers should
// use DefaultHealthPolicy unless they explicitly configure every threshold.
type HealthPolicy struct {
	Window          time.Duration
	MinRequests     float64
	MaxErrorRatePct float64
	MaxP95LatencyMs float64
}

// DefaultHealthPolicy is intentionally conservative. Five minutes gives
// Prometheus enough samples for the gateway histograms while the request
// floor prevents an idle fleet from being treated as healthy evidence.
var DefaultHealthPolicy = HealthPolicy{
	Window:          5 * time.Minute,
	MinRequests:     20,
	MaxErrorRatePct: 5,
	MaxP95LatencyMs: 2000,
}

func (p HealthPolicy) normalized() HealthPolicy {
	if p.Window <= 0 {
		p.Window = DefaultHealthPolicy.Window
	}
	if p.MinRequests <= 0 {
		p.MinRequests = DefaultHealthPolicy.MinRequests
	}
	if p.MaxErrorRatePct <= 0 {
		p.MaxErrorRatePct = DefaultHealthPolicy.MaxErrorRatePct
	}
	if p.MaxP95LatencyMs <= 0 {
		p.MaxP95LatencyMs = DefaultHealthPolicy.MaxP95LatencyMs
	}
	return p
}

// Evaluate returns nil when the snapshot has enough traffic and remains under
// every threshold. A non-nil error is safe to surface to the operator and is
// also the reason recorded by the automatic rollback audit event.
func (p HealthPolicy) Evaluate(snapshot HealthSnapshot) error {
	p = p.normalized()
	if snapshot.Requests < p.MinRequests {
		return fmt.Errorf("runtime config canary has insufficient traffic: %.0f requests, need %.0f", snapshot.Requests, p.MinRequests)
	}
	if snapshot.ErrorRatePct > p.MaxErrorRatePct {
		return fmt.Errorf("runtime config canary error rate %.2f%% exceeds %.2f%%", snapshot.ErrorRatePct, p.MaxErrorRatePct)
	}
	if snapshot.P95LatencyMs > p.MaxP95LatencyMs {
		return fmt.Errorf("runtime config canary p95 latency %.0fms exceeds %.0fms", snapshot.P95LatencyMs, p.MaxP95LatencyMs)
	}
	return nil
}

// PrometheusHealthProvider reads the fleet-level gateway SLO signals exposed
// by the standard Gregale metrics. Query errors are returned as unavailable;
// the controller fails safe and leaves the canary unchanged in that case.
type PrometheusHealthProvider struct {
	Client PromQLClient
	Policy HealthPolicy
	Now    func() time.Time
}

func (p PrometheusHealthProvider) Snapshot(ctx context.Context) (HealthSnapshot, error) {
	if p.Client == nil {
		return HealthSnapshot{}, fmt.Errorf("prometheus health provider is not configured")
	}
	policy := p.Policy.normalized()
	window := promDuration(policy.Window)
	queries := []string{
		fmt.Sprintf(`sum(increase(gateway_requests_total[%s]))`, window),
		fmt.Sprintf(`sum(rate(gateway_requests_total{code=~"5.."}[%s])) / clamp_min(sum(rate(gateway_requests_total[%s])), 1) * 100`, window, window),
		fmt.Sprintf(`histogram_quantile(0.95, sum(rate(gateway_request_duration_seconds_bucket[%s])) by (le)) * 1000`, window),
	}
	values := make([]float64, len(queries))
	for i, query := range queries {
		value, err := p.Client.QueryScalar(ctx, query)
		if err != nil {
			return HealthSnapshot{}, fmt.Errorf("query rollout health signal %d: %w", i, err)
		}
		values[i] = value
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return HealthSnapshot{}, fmt.Errorf("query rollout health signal %d returned non-finite value", i)
		}
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	return HealthSnapshot{
		Requests:     values[0],
		ErrorRatePct: values[1],
		P95LatencyMs: values[2],
		ObservedAt:   now,
	}, nil
}

func promDuration(value time.Duration) string {
	// Prometheus accepts Go-style durations but does not accept a fractional
	// number of seconds in a range selector. The policy windows are expected to
	// be minute-scale; round up so a sub-minute test/config still produces a
	// valid selector.
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
