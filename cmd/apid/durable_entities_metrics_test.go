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
	m.observeResult("outbox", durableentity.Result{}, durableentity.ErrOutboxBackoff)
	m.observeResult("outbox", durableentity.Result{}, durableentity.ErrOutboxExhausted)
	if testutil.ToFloat64(m.outcomes.WithLabelValues("outbox", "backoff")) != 1 || testutil.ToFloat64(m.outcomes.WithLabelValues("outbox", "exhausted")) != 1 {
		t.Fatal("outbox recovery outcomes not recorded")
	}
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
				if name := label.GetName(); name != "kind" && name != "operation" && name != "outcome" && name != "target" {
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
	for _, key := range []string{"gregale/durable-entities/v1/probes/id", "gregale/durable-entities/v1/entities/id/snapshots/1/id.json", "gregale/durable-entities/v1/entities/id/receipts/1/nodes/id", "gregale/durable-entities/v1/maintenance/id.json", "gregale/durable-entities/v1/entities/id/maintenance.json", "gregale/durable-entities/v1/outbox-index/id.json"} {
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

func TestDurableEntityRecoveryOutcomesPreserveUncertainty(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"rejected", durableentity.ErrRestoreRejected, "rejected"},
		{"version conflict", durableentity.ErrRestoreObsolete, "conflict"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"cancel", context.Canceled, "cancelled"},
		{"corrupt missing", errors.Join(durableentity.ErrNotFound, durableentity.ErrCorrupt), "corrupt"},
		{"missing", durableentity.ErrNotFound, "missing"},
		{"lost write deadline", errors.Join(durableentity.ErrUncertain, context.DeadlineExceeded), "uncertain"},
		{"lost write cancel", errors.Join(durableentity.ErrUncertain, context.Canceled), "uncertain"},
		{"deployment changed", durableEntityValidationDeploymentProblem(), "conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := durableEntityOutcome(tc.err); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestDurableEntityLatencySamplesHaveNoIdentityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := newDurableEntityMetrics(registry, "apid")
	reused := newDurableEntityMetrics(registry, "apid")
	if m.duration != reused.duration || m.storageDuration != reused.storageDuration {
		t.Fatal("latency collectors not reused")
	}
	var absent *durableEntityMetrics
	absent.observeDuration("restore", time.Now())
	m.observeDuration("restore", time.Now().Add(-time.Second))
	m.observeDuration("customer:secret-key", time.Now())
	store := durableEntityObservedStore{ObjectStore: &entityTestBucket{objects: map[string]entityTestObject{}}, metrics: func() *durableEntityMetrics { return m }}
	if _, _, err := store.Get(t.Context(), "private/missing", 1024); !errors.Is(err, durableentity.ErrNotFound) {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, family := range families {
		if family.GetName() != "apid_durable_entity_operation_duration_seconds" && family.GetName() != "apid_durable_entity_storage_duration_seconds" {
			continue
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != 1 || metric.Label[0].GetName() != "operation" {
				t.Fatal("identity label", metric)
			}
			operation := metric.Label[0].GetValue()
			if operation != "restore" && operation != "get" {
				t.Fatal("unexpected operation label", operation)
			}
			if metric.Histogram.GetSampleCount() != 1 {
				t.Fatal("duplicate or missing sample", metric)
			}
			found[operation] = true
		}
	}
	if !found["restore"] || !found["get"] {
		t.Fatal("latency families missing", found)
	}
}

func TestDurableEntityStorageMetricsKeepUnknownWritesUncertain(t *testing.T) {
	m := newDurableEntityMetrics(prometheus.NewRegistry(), "apid")
	store := durableEntityObservedStore{metrics: func() *durableEntityMetrics { return m }}
	store.record("put", time.Now(), errors.Join(durableentity.ErrUncertain, durableentity.ErrNotFound, context.DeadlineExceeded))
	if got := testutil.ToFloat64(m.operations.WithLabelValues("put", "uncertain")); got != 1 {
		t.Fatal("unknown write misclassified", got)
	}
	if got := testutil.ToFloat64(m.operations.WithLabelValues("put", "missing")); got != 0 {
		t.Fatal("unknown write classified missing", got)
	}
}
