package wire

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestJobDispatchMetricsUseAClosedVocabulary(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewJobDispatchMetrics(reg, "schedd")
	// Every result is exported at zero so a rate over it exists from boot.
	if got := testutil.CollectAndCount(reg, "schedd_job_dispatch_total"); got != len(jobDispatchResults) {
		t.Fatalf("pre-instantiated series = %d, want %d", got, len(jobDispatchResults))
	}
	metrics.Observe(JobDispatchNodeCapacity)
	metrics.Observe("account-123")
	if got := testutil.ToFloat64(metrics.decisions.WithLabelValues(JobDispatchNodeCapacity)); got != 1 {
		t.Fatalf("node_capacity = %v", got)
	}
	if got := testutil.ToFloat64(metrics.decisions.WithLabelValues(JobDispatchWakeError)); got != 1 {
		t.Fatalf("unknown result counted as wake_error = %v", got)
	}
	if got := testutil.CollectAndCount(reg, "schedd_job_dispatch_total"); got != len(jobDispatchResults) {
		t.Fatalf("an unknown result added a series: %d", got)
	}
	var unwired *JobDispatchMetrics
	unwired.Observe(JobDispatchStarted)
}
