package realtime

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

type authMetricMode uint8

const (
	authMetricModeNone authMetricMode = iota
	authMetricModeStaticBearer
	authMetricModeOIDCJWT
	authMetricModeCustom
	authMetricModeCount
)

var authMetricModeLabels = [...]string{"none", "static_bearer", "oidc_jwt", "custom"}

type authMetricOutcome uint8

const (
	authMetricOutcomeAccepted authMetricOutcome = iota
	authMetricOutcomeRejected
	authMetricOutcomeCount
)

var authMetricOutcomeLabels = [...]string{"accepted", "rejected"}

// StatsCollector exposes the bounded, process-local realtime counters from a
// Manager on an operator-owned Prometheus registry. The counters deliberately
// retain the process-local scope of Manager. Fleet dashboards should sum them
// across instances, while current_connections should be aggregated as a
// gauge only when the dashboard wants total live sockets.
type StatsCollector struct {
	manager *Manager

	currentConnections  *prometheus.Desc
	acceptedConnections *prometheus.Desc
	rejectedConnections *prometheus.Desc
	receivedMessages    *prometheus.Desc
	receivedBytes       *prometheus.Desc
	sentMessages        *prometheus.Desc
	sentBytes           *prometheus.Desc
	droppedMessages     *prometheus.Desc
	callbackErrors      *prometheus.Desc
	authOutcomes        *prometheus.Desc
}

// authOutcomeCounters is intentionally an array, not a map. The dimensions
// are closed at compile time so an endpoint, app, principal, or token can
// never create a new Prometheus series.
type authOutcomeCounters [authMetricModeCount][authMetricOutcomeCount]atomic.Uint64

func authMetricModeForEndpoint(endpoint Endpoint) authMetricMode {
	// A custom authorizer is the effective gate when present. This keeps one
	// request represented by one outcome even when it also has a client-auth
	// policy configured.
	if endpoint.Authorize != nil {
		return authMetricModeCustom
	}
	switch endpoint.ClientAuth.Mode {
	case AuthModeStaticBearer:
		return authMetricModeStaticBearer
	case AuthModeOIDCJWT:
		return authMetricModeOIDCJWT
	default:
		return authMetricModeNone
	}
}

func (m *Manager) recordAuthOutcome(mode authMetricMode, outcome authMetricOutcome) {
	if m == nil || mode >= authMetricModeCount || outcome >= authMetricOutcomeCount {
		return
	}
	m.authOutcomes[mode][outcome].Add(1)
}

// NewStatsCollector binds a Manager's safe point-in-time stats to a
// Prometheus collector. The collector does not retain endpoint IDs, app IDs,
// principals, or connection IDs, so its series count is fixed.
func NewStatsCollector(manager *Manager) prometheus.Collector {
	const subsystem = "realtimed"
	return &StatsCollector{
		manager:             manager,
		currentConnections:  prometheus.NewDesc(subsystem+"_current_connections", "Current managed realtime connections.", nil, nil),
		acceptedConnections: prometheus.NewDesc(subsystem+"_accepted_connections_total", "Managed realtime connections accepted since process start.", nil, nil),
		rejectedConnections: prometheus.NewDesc(subsystem+"_rejected_connections_total", "Managed realtime connections rejected since process start.", nil, nil),
		receivedMessages:    prometheus.NewDesc(subsystem+"_received_messages_total", "Realtime messages received since process start.", nil, nil),
		receivedBytes:       prometheus.NewDesc(subsystem+"_received_bytes_total", "Bytes received from realtime clients since process start.", nil, nil),
		sentMessages:        prometheus.NewDesc(subsystem+"_sent_messages_total", "Realtime messages sent to clients since process start.", nil, nil),
		sentBytes:           prometheus.NewDesc(subsystem+"_sent_bytes_total", "Bytes sent to realtime clients since process start.", nil, nil),
		droppedMessages:     prometheus.NewDesc(subsystem+"_dropped_messages_total", "Realtime messages dropped because an outbound queue was full.", nil, nil),
		callbackErrors:      prometheus.NewDesc(subsystem+"_callback_errors_total", "Realtime lifecycle callback failures since process start.", nil, nil),
		authOutcomes:        prometheus.NewDesc(subsystem+"_auth_outcomes_total", "Realtime client authentication outcomes since process start.", []string{"mode", "outcome"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *StatsCollector) Describe(ch chan<- *prometheus.Desc) {
	if c == nil {
		return
	}
	for _, desc := range c.descs() {
		ch <- desc
	}
}

// Collect implements prometheus.Collector.
func (c *StatsCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil {
		return
	}
	stats := c.manager.Stats()
	ch <- prometheus.MustNewConstMetric(c.currentConnections, prometheus.GaugeValue, float64(stats.CurrentConnections))
	ch <- prometheus.MustNewConstMetric(c.acceptedConnections, prometheus.CounterValue, float64(stats.AcceptedConnections))
	ch <- prometheus.MustNewConstMetric(c.rejectedConnections, prometheus.CounterValue, float64(stats.RejectedConnections))
	ch <- prometheus.MustNewConstMetric(c.receivedMessages, prometheus.CounterValue, float64(stats.ReceivedMessages))
	ch <- prometheus.MustNewConstMetric(c.receivedBytes, prometheus.CounterValue, float64(stats.ReceivedBytes))
	ch <- prometheus.MustNewConstMetric(c.sentMessages, prometheus.CounterValue, float64(stats.SentMessages))
	ch <- prometheus.MustNewConstMetric(c.sentBytes, prometheus.CounterValue, float64(stats.SentBytes))
	ch <- prometheus.MustNewConstMetric(c.droppedMessages, prometheus.CounterValue, float64(stats.DroppedMessages))
	ch <- prometheus.MustNewConstMetric(c.callbackErrors, prometheus.CounterValue, float64(stats.CallbackErrors))
	for mode := authMetricMode(0); mode < authMetricModeCount; mode++ {
		for outcome := authMetricOutcome(0); outcome < authMetricOutcomeCount; outcome++ {
			ch <- prometheus.MustNewConstMetric(c.authOutcomes, prometheus.CounterValue,
				float64(c.manager.authOutcomes[mode][outcome].Load()), authMetricModeLabels[mode], authMetricOutcomeLabels[outcome])
		}
	}
}

func (c *StatsCollector) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		c.currentConnections,
		c.acceptedConnections,
		c.rejectedConnections,
		c.receivedMessages,
		c.receivedBytes,
		c.sentMessages,
		c.sentBytes,
		c.droppedMessages,
		c.callbackErrors,
		c.authOutcomes,
	}
}
