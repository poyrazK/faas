// adr: 937
package durable

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestHealthRotationsAggregateWithoutIdentityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry, "apid")
	r := HealthRotation{}
	at := time.Now().Add(-time.Hour)
	r.add(durableentity.HealthPage{NextCursor: "page2", Samples: []durableentity.HealthSample{{AlarmPending: true, AlarmExhausted: true, AlarmAt: &at, OutboxPending: 2, OutboxUnknownAge: 1, OldestOutboxAt: &at}}})
	if r.cursor != "page2" || r.entities != 1 {
		t.Fatal(r)
	}
	r.add(durableentity.HealthPage{Samples: []durableentity.HealthSample{{OutboxPending: 3, OutboxExhausted: true}}})
	m.health.publish(r, time.Now())
	if testutil.ToFloat64(m.health.pending.WithLabelValues("outbox")) != 5 || testutil.ToFloat64(m.health.exhausted.WithLabelValues("alarm")) != 1 || testutil.ToFloat64(m.health.exhausted.WithLabelValues("outbox")) != 1 || testutil.ToFloat64(m.health.unknownAge.WithLabelValues()) != 1 {
		t.Fatal("rotation aggregation lost observations")
	}
	for _, err := range []error{nil, durableentity.ErrRecoveryObsolete, durableentity.ErrBusy, durableentity.ErrUncertain, errors.New("private")} {
		m.observeResult("retry_outbox", durableentity.Result{}, err)
	}
	for _, outcome := range []string{"success", "conflict", "busy", "uncertain", "failed"} {
		if testutil.ToFloat64(m.outcomes.WithLabelValues("retry_outbox", outcome)) != 1 {
			t.Fatal(outcome)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "operation", "outcome", "kind", "target":
				default:
					t.Fatal("identity label", label.GetName())
				}
			}
		}
	}
}
