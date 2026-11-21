package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/db"
)

// notificationOutboxBacklogInterval is how often schedd reads the outbox
// backlog. The query reads only pending and processing rows (the claim
// indexes' partial predicate), so it stays cheap on every schedd.
const notificationOutboxBacklogInterval = 30 * time.Second

// notificationOutboxMetrics exports the durable handoff backlog per channel.
// Every schedd reads the same shared table; the alert takes the max across
// them. Production 2026-11-16: a credential rotation's restart sat pending
// for hours and nothing fired, because no metric reported an undelivered row.
type notificationOutboxMetrics struct {
	undelivered *prometheus.GaugeVec
	oldest      *prometheus.GaugeVec
}

func newNotificationOutboxMetrics(reg prometheus.Registerer) *notificationOutboxMetrics {
	m := &notificationOutboxMetrics{
		undelivered: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "faas_notification_outbox_undelivered",
			Help: "Durable notification outbox rows still pending or processing, by channel.",
		}, []string{"channel"}),
		oldest: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "faas_notification_outbox_oldest_undelivered_seconds",
			Help: "Age of the oldest pending or processing durable notification outbox row, by channel.",
		}, []string{"channel"}),
	}
	reg.MustRegister(m.undelivered, m.oldest)
	return m
}

// observe replaces every series with the current backlog, so a channel that
// drained reads zero rather than its last value. Channels outside the durable
// set share one label value, which keeps the label set bounded.
func (m *notificationOutboxMetrics) observe(backlogs []db.NotificationOutboxBacklog) {
	undelivered, oldest := map[string]float64{}, map[string]float64{}
	for _, channel := range db.DurableNotificationChannels() {
		undelivered[channel], oldest[channel] = 0, 0
	}
	for _, b := range backlogs {
		channel := b.Channel
		if !db.IsDurableNotificationChannel(channel) {
			channel = "other"
		}
		undelivered[channel] += float64(b.Undelivered)
		oldest[channel] = max(oldest[channel], b.Oldest.Seconds())
	}
	m.undelivered.Reset()
	m.oldest.Reset()
	for channel, n := range undelivered {
		m.undelivered.WithLabelValues(channel).Set(n)
		m.oldest.WithLabelValues(channel).Set(oldest[channel])
	}
}

// run reads the backlog until ctx ends. A failed read keeps the previous
// values: an outage of this exporter is visible through the scrape, and
// zeroing would read as a drained outbox.
func (m *notificationOutboxMetrics) run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) {
	ticker := time.NewTicker(notificationOutboxBacklogInterval)
	defer ticker.Stop()
	for {
		backlogs, err := db.NotificationOutboxBacklogs(ctx, pool)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("schedd: read notification outbox backlog", "err", err)
			}
		} else {
			m.observe(backlogs)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
