// adr: 712
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDurableEntityMetricsReuseCollectorsAndBoundLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	first := newDurableEntityMetrics(registry, "apid")
	m := newDurableEntityMetrics(registry, "apid")
	if first.operations != m.operations || first.alarmDelay != m.alarmDelay {
		t.Fatal("collectors not reused")
	}
	var absent *durableEntityMetrics
	absent.observeResult("invoke", durableentity.Result{}, nil)
	absent.observeMaintenance(durableentity.MaintenanceResult{}, nil)
	absent.observeAlarmDelay(time.Now())
	m.observeResult("invoke", durableentity.Result{}, durableentity.ErrBusy)
	m.observeResult("invoke", durableentity.Result{Replayed: true}, nil)
	m.observeMaintenance(durableentity.MaintenanceResult{Busy: 1}, nil)
	m.observeMaintenance(durableentity.MaintenanceResult{Failed: 1}, nil)
	m.observeMaintenance(durableentity.MaintenanceResult{Recovered: true, SweepCompleted: true, Cleanup: durableentity.CleanupResult{Deleted: 3, Retained: 4, Failed: 1}}, nil)
	m.observeAlarmDelay(time.Now().Add(-time.Minute))
	if testutil.ToFloat64(m.cleanup.WithLabelValues("deleted")) != 3 || testutil.ToFloat64(m.cleanup.WithLabelValues("failed")) != 1 || testutil.ToFloat64(m.outcomes.WithLabelValues("maintenance", "failed")) != 2 || testutil.ToFloat64(m.recoveries.WithLabelValues()) != 1 || testutil.ToFloat64(m.lastSweep.WithLabelValues()) == 0 {
		t.Fatal("maintenance outcomes not recorded")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if name := label.GetName(); name != "kind" && name != "operation" && name != "outcome" {
					t.Fatal("unbounded identity label", name)
				}
			}
		}
	}
}

func TestDurableEntityObservedStorePreservesCapabilitiesAndUnknownOutcomes(t *testing.T) {
	m := newDurableEntityMetrics(prometheus.NewRegistry(), "apid")
	bucket := &entityTestBucket{objects: map[string]entityTestObject{}}
	store := durableEntityObservedStore{ObjectStore: bucket, metrics: func() *durableEntityMetrics { return m }}
	key := "gregale/durable-entities/v1/entities/private/manifest.json"
	body := []byte(`{"state_version":1}`)
	if _, err := store.Put(t.Context(), key, body, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), key, body, ""); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal(err)
	}
	_, etag, err := store.Get(t.Context(), key, 1024)
	if err != nil {
		t.Fatal(err)
	}
	bucket.lose.Store(true)
	if _, err := store.Put(t.Context(), key, body, etag); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal("unknown dispatched write became definite", err)
	}
	if _, err := store.ListEntityPrefixes(t.Context(), "gregale/durable-entities/v1/entities/", "", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListEntityObjects(t.Context(), "gregale/durable-entities/v1/entities/", "", 32); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEntityObject(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(m.uploadedBytes.WithLabelValues("manifest")) != float64(len(body)) || testutil.ToFloat64(m.operations.WithLabelValues("put", "conflict")) != 1 || testutil.ToFloat64(m.operations.WithLabelValues("put", "uncertain")) != 1 || testutil.ToFloat64(m.operations.WithLabelValues("delete", "success")) != 1 {
		t.Fatal("storage counters do not describe acknowledged operations")
	}
	for _, key := range []string{"gregale/durable-entities/v1/probes/id", "gregale/durable-entities/v1/entities/id/snapshots/1/id.json", "gregale/durable-entities/v1/entities/id/receipts/1/nodes/id", "gregale/durable-entities/v1/maintenance/id.json", "gregale/durable-entities/v1/entities/id/maintenance.json"} {
		if kind := durableEntityObjectKind(key); kind == "other" || strings.Contains(kind, "/") {
			t.Fatal(key, kind)
		}
	}
	// Rebinding through the existing server hook changes the active registry.
	s := &server{}
	s.WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid"))
	if s.durableEntityMetrics == nil {
		t.Fatal("metrics hook did not bind collectors")
	}
	s.WithOpsMetrics(context.Background(), nil)
	if s.durableEntityMetrics != nil {
		t.Fatal("metrics hook did not clear collectors")
	}
}
