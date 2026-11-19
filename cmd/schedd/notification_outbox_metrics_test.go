package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/db"
)

func TestNotificationOutboxMetricsReportTheBacklogAndClearWhenItDrains(t *testing.T) {
	m := newNotificationOutboxMetrics(prometheus.NewRegistry())
	m.observe([]db.NotificationOutboxBacklog{
		{Channel: db.NotifyRuntimeConfigRestart, Undelivered: 2, Oldest: 95 * time.Minute},
		{Channel: "unlisted_a", Undelivered: 1, Oldest: time.Minute},
		{Channel: "unlisted_b", Undelivered: 3, Oldest: 4 * time.Minute},
	})
	for _, c := range []struct {
		channel     string
		undelivered float64
		oldest      float64
	}{
		{db.NotifyRuntimeConfigRestart, 2, 95 * 60},
		{"other", 4, 4 * 60},
		{db.NotifyAppWake, 0, 0},
	} {
		if got := testutil.ToFloat64(m.undelivered.WithLabelValues(c.channel)); got != c.undelivered {
			t.Errorf("%s undelivered = %v, want %v", c.channel, got, c.undelivered)
		}
		if got := testutil.ToFloat64(m.oldest.WithLabelValues(c.channel)); got != c.oldest {
			t.Errorf("%s oldest = %v, want %v", c.channel, got, c.oldest)
		}
	}
	m.observe(nil)
	if got := testutil.ToFloat64(m.oldest.WithLabelValues(db.NotifyRuntimeConfigRestart)); got != 0 {
		t.Errorf("a drained channel still reports %v s", got)
	}
	if n := testutil.CollectAndCount(m.oldest); n != len(db.DurableNotificationChannels()) {
		t.Errorf("drained outbox exports %d series, want one per durable channel (%d)", n, len(db.DurableNotificationChannels()))
	}
}
