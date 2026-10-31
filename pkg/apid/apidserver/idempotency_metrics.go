package apidserver

import (
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// idempotencyMetrics counts Idempotency-Keys refused because they were reused
// for a different request (ADR-20261022204036). The route label is the
// matched route pattern, a closed set bounded by the route table.
type idempotencyMetrics struct {
	registry  prometheus.Registerer
	keyReused *prometheus.CounterVec
}

func newIdempotencyMetrics(reg prometheus.Registerer, prefix string) *idempotencyMetrics {
	m := &idempotencyMetrics{registry: reg, keyReused: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_idempotency_key_reused_total",
		Help: "Requests refused with idempotency_key_reused because their Idempotency-Key was bound to a different request.",
	}, []string{"route"})}
	if err := reg.Register(m.keyReused); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
		m.keyReused = already.ExistingCollector.(*prometheus.CounterVec)
	}
	return m
}

func (m *idempotencyMetrics) observeKeyReused(r *http.Request) {
	if m == nil {
		return
	}
	route := r.Pattern
	if route == "" {
		route = "unknown"
	}
	m.keyReused.WithLabelValues(route).Inc()
}
