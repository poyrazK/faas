package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsExposeBoundedLifecycleAndUsageSignals(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "apid", true)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	metrics.SetProvisioningEnabled(true)
	metrics.ObserveCanary(true)
	metrics.ObserveCanary(false)
	metrics.ObserveAdmission(ErrUsageStale)
	metrics.ObserveReconcile(ReconcileObservation{
		Operation: StateProvisioning, Outcome: OutcomeCompleted, Duration: 2 * time.Second,
	})
	metrics.ObserveBindingReconcile(BindingReconcileObservation{
		Operation: BindingStateDeleting, Outcome: OutcomeFailed, Duration: 3 * time.Second,
	})
	metrics.ObserveUsage(UsageCollectionObservation{Outcome: "recorded"})
	metrics.ObserveUsage(UsageCollectionObservation{Outcome: "deferred"})
	completedAt := time.Unix(1_725_000_000, 0).UTC()
	metrics.ObserveUsageSweep(UsageCollectionSummary{
		Discovered: 2, Recorded: 1, Deferred: 1, Enabled: true, CompletedAt: completedAt,
	}, errors.New("provider unavailable"))

	assertMetric := func(collector prometheus.Collector, want float64, name string) {
		t.Helper()
		if got := testutil.ToFloat64(collector); got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertMetric(metrics.reconcileTotal.WithLabelValues("database", "provisioning", "completed"), 1, "database reconcile")
	assertMetric(metrics.reconcileTotal.WithLabelValues("binding", "deleting", "failed"), 1, "binding reconcile")
	assertMetric(metrics.usageDatabaseTotal.WithLabelValues("recorded"), 1, "usage recorded")
	assertMetric(metrics.usageDatabaseTotal.WithLabelValues("deferred"), 1, "usage deferred")
	assertMetric(metrics.usageSweepTotal.WithLabelValues("error"), 1, "usage sweep error")
	assertMetric(metrics.canaryAdmissionTotal.WithLabelValues("allowed"), 1, "canary allowed")
	assertMetric(metrics.canaryAdmissionTotal.WithLabelValues("denied"), 1, "canary denied")
	assertMetric(metrics.admissionDeniedTotal.WithLabelValues("usage_stale"), 1, "stale admission")
	assertMetric(metrics.provisioningEnabled, 1, "provisioning gate")
	assertMetric(metrics.usagePolicyEnabled, 1, "usage policy")
	assertMetric(metrics.usageDatabases.WithLabelValues("discovered"), 2, "usage discovered")
	assertMetric(metrics.usageDatabases.WithLabelValues("deferred"), 1, "usage deferred gauge")

	// A degraded sweep must not advance freshness. A complete follow-up does.
	if got := testutil.ToFloat64(metrics.usageLastSuccess); got != 0 {
		t.Fatalf("last success after error = %v, want 0", got)
	}
	metrics.ObserveUsageSweep(UsageCollectionSummary{Enabled: true, CompletedAt: completedAt}, nil)
	assertMetric(metrics.usageLastSuccess, float64(completedAt.Unix()), "usage last success")

	// Every public label is a closed set and is pre-instantiated at boot.
	if got := testutil.ToFloat64(metrics.reconcileTotal.WithLabelValues("database", "deleting", "deferred")); got != 0 {
		t.Fatalf("pre-instantiated deferred series = %v, want 0", got)
	}
}

func TestMetricsRejectEmptyPrefixAndAllowNilRegisterer(t *testing.T) {
	if metrics, err := NewMetrics(nil, "apid", false); err != nil || metrics != nil {
		t.Fatalf("nil registerer = metrics %v err %v, want nil nil", metrics, err)
	}
	if _, err := NewMetrics(prometheus.NewRegistry(), "", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty prefix err = %v, want ErrInvalid", err)
	}
}
