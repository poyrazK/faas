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

	currentConnections              *prometheus.Desc
	currentResumeSubscriptions      *prometheus.Desc
	acceptedConnections             *prometheus.Desc
	rejectedConnections             *prometheus.Desc
	receivedMessages                *prometheus.Desc
	receivedBytes                   *prometheus.Desc
	sentMessages                    *prometheus.Desc
	sentBytes                       *prometheus.Desc
	droppedMessages                 *prometheus.Desc
	callbackErrors                  *prometheus.Desc
	callbackOutboxFull              *prometheus.Desc
	callbackOutboxAdmissionErrors   *prometheus.Desc
	callbackUnpersistedFailures     *prometheus.Desc
	callbackPending                 *prometheus.Desc
	callbackPendingBytes            *prometheus.Desc
	callbackPendingCapacityBytes    *prometheus.Desc
	callbackReplayReady             *prometheus.Desc
	callbackReplayDelayed           *prometheus.Desc
	callbackReplayAttempts          *prometheus.Desc
	callbackReplayDeliveries        *prometheus.Desc
	callbackOldestPendingAge        *prometheus.Desc
	callbackDeadLetters             *prometheus.Desc
	callbackDeadLetterBytes         *prometheus.Desc
	callbackDeadLetterCapacityBytes *prometheus.Desc
	callbackDeadLetterEvictions     *prometheus.Desc
	callbackDeadLetterLastEviction  *prometheus.Desc
	callbackDeadLetterDiscards      *prometheus.Desc
	authOutcomes                    *prometheus.Desc
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
		manager:                         manager,
		currentConnections:              prometheus.NewDesc(subsystem+"_current_connections", "Current managed realtime connections.", nil, nil),
		currentResumeSubscriptions:      prometheus.NewDesc(subsystem+"_current_resume_subscriptions", "Current v2 retained-channel subscriptions on this realtime node.", nil, nil),
		acceptedConnections:             prometheus.NewDesc(subsystem+"_accepted_connections_total", "Managed realtime connections accepted since process start.", nil, nil),
		rejectedConnections:             prometheus.NewDesc(subsystem+"_rejected_connections_total", "Managed realtime connections rejected since process start.", nil, nil),
		receivedMessages:                prometheus.NewDesc(subsystem+"_received_messages_total", "Realtime messages received since process start.", nil, nil),
		receivedBytes:                   prometheus.NewDesc(subsystem+"_received_bytes_total", "Bytes received from realtime clients since process start.", nil, nil),
		sentMessages:                    prometheus.NewDesc(subsystem+"_sent_messages_total", "Realtime messages sent to clients since process start.", nil, nil),
		sentBytes:                       prometheus.NewDesc(subsystem+"_sent_bytes_total", "Bytes sent to realtime clients since process start.", nil, nil),
		droppedMessages:                 prometheus.NewDesc(subsystem+"_dropped_messages_total", "Realtime messages dropped because an outbound queue was full.", nil, nil),
		callbackErrors:                  prometheus.NewDesc(subsystem+"_callback_errors_total", "Realtime lifecycle callback failures since process start.", nil, nil),
		callbackOutboxFull:              prometheus.NewDesc(subsystem+"_callback_outbox_full_total", "Callback events rejected because the durable outbox remained full until their admission deadline.", nil, nil),
		callbackOutboxAdmissionErrors:   prometheus.NewDesc(subsystem+"_callback_outbox_admission_errors_total", "Callback events rejected because durable outbox admission failed for a reason other than capacity exhaustion.", nil, nil),
		callbackUnpersistedFailures:     prometheus.NewDesc(subsystem+"_callback_unpersisted_failures_total", "Message or disconnect callbacks failed when no durable outbox was configured.", nil, nil),
		callbackPending:                 prometheus.NewDesc(subsystem+"_callback_pending", "Pending durable realtime callbacks.", nil, nil),
		callbackPendingBytes:            prometheus.NewDesc(subsystem+"_callback_pending_bytes", "Bytes in the pending callback outbox.", nil, nil),
		callbackPendingCapacityBytes:    prometheus.NewDesc(subsystem+"_callback_pending_capacity_bytes", "Configured maximum bytes for the pending callback outbox.", nil, nil),
		callbackReplayReady:             prometheus.NewDesc(subsystem+"_callback_replay_ready", "Pending per-connection callback heads eligible for replay now.", nil, nil),
		callbackReplayDelayed:           prometheus.NewDesc(subsystem+"_callback_replay_delayed", "Pending per-connection callback heads waiting for their scheduled retry time.", nil, nil),
		callbackReplayAttempts:          prometheus.NewDesc(subsystem+"_callback_replay_attempts_total", "Durable callback replay delivery attempts since process start, including failed attempts.", nil, nil),
		callbackReplayDeliveries:        prometheus.NewDesc(subsystem+"_callback_replay_deliveries_total", "Callbacks successfully replayed from the durable outbox since process start.", nil, nil),
		callbackOldestPendingAge:        prometheus.NewDesc(subsystem+"_callback_oldest_pending_age_seconds", "Age of the oldest callback currently pending in the durable outbox, or zero when empty.", nil, nil),
		callbackDeadLetters:             prometheus.NewDesc(subsystem+"_callback_dead_letters", "Retained callback dead letters.", nil, nil),
		callbackDeadLetterBytes:         prometheus.NewDesc(subsystem+"_callback_dead_letter_bytes", "Bytes of retained callback dead letters.", nil, nil),
		callbackDeadLetterCapacityBytes: prometheus.NewDesc(subsystem+"_callback_dead_letter_capacity_bytes", "Configured maximum bytes of retained callback dead letters.", nil, nil),
		callbackDeadLetterEvictions:     prometheus.NewDesc(subsystem+"_callback_dead_letter_evictions_total", "Callback dead letters evicted by byte retention since process start.", nil, nil),
		callbackDeadLetterLastEviction:  prometheus.NewDesc(subsystem+"_callback_dead_letter_last_eviction_timestamp_seconds", "Unix timestamp of the most recent callback dead-letter eviction, or zero if none.", nil, nil),
		callbackDeadLetterDiscards:      prometheus.NewDesc(subsystem+"_callback_dead_letter_discards_total", "Callback dead letters discarded by an operator since process start.", nil, nil),
		authOutcomes:                    prometheus.NewDesc(subsystem+"_auth_outcomes_total", "Realtime client authentication outcomes since process start.", []string{"mode", "outcome"}, nil),
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
	ch <- prometheus.MustNewConstMetric(c.currentResumeSubscriptions, prometheus.GaugeValue, float64(stats.CurrentResumeSubscriptions))
	ch <- prometheus.MustNewConstMetric(c.acceptedConnections, prometheus.CounterValue, float64(stats.AcceptedConnections))
	ch <- prometheus.MustNewConstMetric(c.rejectedConnections, prometheus.CounterValue, float64(stats.RejectedConnections))
	ch <- prometheus.MustNewConstMetric(c.receivedMessages, prometheus.CounterValue, float64(stats.ReceivedMessages))
	ch <- prometheus.MustNewConstMetric(c.receivedBytes, prometheus.CounterValue, float64(stats.ReceivedBytes))
	ch <- prometheus.MustNewConstMetric(c.sentMessages, prometheus.CounterValue, float64(stats.SentMessages))
	ch <- prometheus.MustNewConstMetric(c.sentBytes, prometheus.CounterValue, float64(stats.SentBytes))
	ch <- prometheus.MustNewConstMetric(c.droppedMessages, prometheus.CounterValue, float64(stats.DroppedMessages))
	ch <- prometheus.MustNewConstMetric(c.callbackErrors, prometheus.CounterValue, float64(stats.CallbackErrors))
	ch <- prometheus.MustNewConstMetric(c.callbackOutboxFull, prometheus.CounterValue, float64(stats.CallbackOutboxFull))
	ch <- prometheus.MustNewConstMetric(c.callbackOutboxAdmissionErrors, prometheus.CounterValue, float64(stats.CallbackOutboxAdmissionErrors))
	ch <- prometheus.MustNewConstMetric(c.callbackUnpersistedFailures, prometheus.CounterValue, float64(stats.CallbackUnpersistedFailures))
	ch <- prometheus.MustNewConstMetric(c.callbackPending, prometheus.GaugeValue, float64(stats.CallbackPending))
	ch <- prometheus.MustNewConstMetric(c.callbackPendingBytes, prometheus.GaugeValue, float64(stats.CallbackPendingBytes))
	ch <- prometheus.MustNewConstMetric(c.callbackPendingCapacityBytes, prometheus.GaugeValue, float64(stats.CallbackPendingCapacityBytes))
	ch <- prometheus.MustNewConstMetric(c.callbackReplayReady, prometheus.GaugeValue, float64(stats.CallbackReplayReady))
	ch <- prometheus.MustNewConstMetric(c.callbackReplayDelayed, prometheus.GaugeValue, float64(stats.CallbackReplayDelayed))
	ch <- prometheus.MustNewConstMetric(c.callbackReplayAttempts, prometheus.CounterValue, float64(stats.CallbackReplayAttempts))
	ch <- prometheus.MustNewConstMetric(c.callbackReplayDeliveries, prometheus.CounterValue, float64(stats.CallbackReplayDeliveries))
	ch <- prometheus.MustNewConstMetric(c.callbackOldestPendingAge, prometheus.GaugeValue, stats.CallbackOldestPendingAgeSeconds)
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetters, prometheus.GaugeValue, float64(stats.CallbackDeadLetters))
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetterBytes, prometheus.GaugeValue, float64(stats.CallbackDeadLetterBytes))
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetterCapacityBytes, prometheus.GaugeValue, float64(stats.CallbackDeadLetterCapacityBytes))
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetterEvictions, prometheus.CounterValue, float64(stats.CallbackDeadLetterEvictions))
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetterLastEviction, prometheus.GaugeValue, float64(stats.CallbackDeadLetterLastEvictionUnix))
	ch <- prometheus.MustNewConstMetric(c.callbackDeadLetterDiscards, prometheus.CounterValue, float64(stats.CallbackDeadLetterDiscards))
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
		c.currentResumeSubscriptions,
		c.acceptedConnections,
		c.rejectedConnections,
		c.receivedMessages,
		c.receivedBytes,
		c.sentMessages,
		c.sentBytes,
		c.droppedMessages,
		c.callbackErrors,
		c.callbackOutboxFull,
		c.callbackOutboxAdmissionErrors,
		c.callbackUnpersistedFailures,
		c.callbackPending,
		c.callbackPendingBytes,
		c.callbackPendingCapacityBytes,
		c.callbackReplayReady,
		c.callbackReplayDelayed,
		c.callbackReplayAttempts,
		c.callbackReplayDeliveries,
		c.callbackOldestPendingAge,
		c.callbackDeadLetters,
		c.callbackDeadLetterBytes,
		c.callbackDeadLetterCapacityBytes,
		c.callbackDeadLetterEvictions,
		c.callbackDeadLetterLastEviction,
		c.callbackDeadLetterDiscards,
		c.authOutcomes,
	}
}
