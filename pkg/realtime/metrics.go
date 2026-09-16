package realtime

import "github.com/prometheus/client_golang/prometheus"

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
	}
}
