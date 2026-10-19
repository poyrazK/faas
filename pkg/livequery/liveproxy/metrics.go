package liveproxy

import "github.com/prometheus/client_golang/prometheus"

// Metrics are realtimed's live-query metrics. Live queries ride ordinary
// realtime connections, so ADR-1323's connection limits and outbound budget
// already cover them; these show what they add, for the same capacity
// review. A nil *Metrics records nothing.
type Metrics struct {
	sessions      prometheus.Gauge
	subscriptions prometheus.Gauge
	upstreams     prometheus.Gauge
	drops         prometheus.Counter
	frames        *prometheus.CounterVec
	bytes         *prometheus.CounterVec
	refusals      *prometheus.CounterVec
}

// NewMetrics registers the metrics with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		sessions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "realtimed_live_sessions",
			Help: "Realtime connections that have used live queries."}),
		subscriptions: prometheus.NewGauge(prometheus.GaugeOpts{Name: "realtimed_live_subscriptions",
			Help: "Live-query subscriptions carried for clients."}),
		upstreams: prometheus.NewGauge(prometheus.GaugeOpts{Name: "realtimed_live_upstreams",
			Help: "Databases whose livequeryd hub this node holds a connection to."}),
		drops: prometheus.NewCounter(prometheus.CounterOpts{Name: "realtimed_live_upstream_drops_total",
			Help: "Hub connections that dropped and were redialed."}),
		frames: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtimed_live_frames_total",
			Help: "Live-query frames carried, by direction (to_client, to_hub)."}, []string{"direction"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtimed_live_bytes_total",
			Help: "Live-query frame bytes carried, by direction (to_client, to_hub)."}, []string{"direction"}),
		refusals: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "realtimed_live_refusals_total",
			Help: "Live-query frames refused in realtimed, by live.error code."}, []string{"code"}),
	}
	reg.MustRegister(m.sessions, m.subscriptions, m.upstreams, m.drops, m.frames, m.bytes, m.refusals)
	return m
}

func (m *Metrics) addSessions(n float64) {
	if m != nil {
		m.sessions.Add(n)
	}
}

func (m *Metrics) addSubscriptions(n float64) {
	if m != nil {
		m.subscriptions.Add(n)
	}
}

func (m *Metrics) addUpstreams(n float64) {
	if m != nil {
		m.upstreams.Add(n)
	}
}

func (m *Metrics) upstreamDropped() {
	if m != nil {
		m.drops.Inc()
	}
}

func (m *Metrics) carried(direction string, bytes int) {
	if m != nil {
		m.frames.WithLabelValues(direction).Inc()
		m.bytes.WithLabelValues(direction).Add(float64(bytes))
	}
}

func (m *Metrics) refused(code string) {
	if m != nil {
		m.refusals.WithLabelValues(code).Inc()
	}
}
