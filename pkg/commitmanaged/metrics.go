package commitmanaged

import (
	"context"
	"time"

	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type ObservationStore interface {
	CommitRelayObservationSummary(context.Context, time.Time) (state.CommitRelayObservation, error)
}

// ObservationCollector reads shared source observations on scrape, so restart,
// pause, rotation and deletion cannot leave process-local source gauges behind.
// No account/source/event IDs are metric labels. Fleet queries must use max,
// because each scheduler observes the same platform ledger.
type ObservationCollector struct {
	store ObservationStore
	descs []*prometheus.Desc
}

func NewObservationCollector(store ObservationStore) *ObservationCollector {
	names := []string{"observation_snapshot_success", "enabled_sources", "unknown_sources", "failing_sources", "pending_events", "blocked_events", "oldest_pending_timestamp_seconds"}
	help := []string{
		"Whether the shared Commit observation snapshot could be read; zero means all observations are unknown.",
		"Enabled Commit sources in the shared platform ledger.",
		"Enabled Commit sources without a fresh known backlog; unknown does not mean empty.",
		"Enabled Commit sources with a fresh unhealthy relay observation, excluding blocked events.",
		"Unaccepted unblocked events across fresh known Commit source observations.",
		"Blocked events across fresh known Commit source observations.",
		"Oldest pending row insertion timestamp across fresh known observations; zero when none is observed. This is not a commit timestamp.",
	}
	c := &ObservationCollector{store: store}
	for i, name := range names {
		c.descs = append(c.descs, prometheus.NewDesc("schedd_commit_relay_"+name, help[i], nil, nil))
	}
	return c
}

func (c *ObservationCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}
}

func (c *ObservationCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := c.store.CommitRelayObservationSummary(ctx, time.Now().UTC().Add(-commitwork.ObservationMaxAge))
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.descs[0], prometheus.GaugeValue, 0)
		return
	}
	values := []float64{1, float64(snapshot.EnabledSources), float64(snapshot.UnknownSources), float64(snapshot.FailingSources), float64(snapshot.PendingEvents), float64(snapshot.BlockedEvents), snapshot.OldestPendingTimestamp}
	for i, value := range values {
		ch <- prometheus.MustNewConstMetric(c.descs[i], prometheus.GaugeValue, value)
	}
}
