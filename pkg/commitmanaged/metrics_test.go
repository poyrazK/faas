package commitmanaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type observationFixture struct {
	snapshot state.CommitRelayObservation
	err      error
}

func (f *observationFixture) CommitRelayObservationSummary(context.Context, time.Time) (state.CommitRelayObservation, error) {
	return f.snapshot, f.err
}

func TestObservationCollectorUnknownAndRecovery(t *testing.T) {
	f := &observationFixture{snapshot: state.CommitRelayObservation{EnabledSources: 2, UnknownSources: 1, PendingEvents: 9, OldestPendingTimestamp: 1234}}
	r := prometheus.NewPedanticRegistry()
	r.MustRegister(NewObservationCollector(f))
	read := func() map[string]float64 {
		t.Helper()
		families, err := r.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, family := range families {
			if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
				t.Fatal("source/account metric cardinality escaped")
			}
			values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
		}
		return values
	}
	if got := read(); got["schedd_commit_relay_unknown_sources"] != 1 || got["schedd_commit_relay_pending_events"] != 9 {
		t.Fatalf("snapshot=%v", got)
	}
	f.err = errors.New("database unavailable")
	if got := read(); len(got) != 1 || got["schedd_commit_relay_observation_snapshot_success"] != 0 {
		t.Fatalf("failure reused stale gauges: %v", got)
	}
	f.err, f.snapshot = nil, state.CommitRelayObservation{}
	if got := read(); got["schedd_commit_relay_observation_snapshot_success"] != 1 || got["schedd_commit_relay_pending_events"] != 0 || got["schedd_commit_relay_enabled_sources"] != 0 {
		t.Fatalf("recovery retained deleted sources: %v", got)
	}
}
