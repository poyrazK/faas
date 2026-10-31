package releasemetrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsRecordDocumentedLabelsOnly(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := New(registry)
	if again := New(registry); again.decisions != m.decisions {
		t.Fatal("registering twice must share the first collectors")
	}
	m.Decision(GuardFreeze, "refused")
	m.Decision(GuardFreeze, "invented")
	m.Decision("invented", "refused")
	if got := testutil.ToFloat64(m.decisions.WithLabelValues(GuardFreeze, "refused")); got != 1 {
		t.Fatalf("freeze refusals = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.decisions); got != len(flatten()) {
		t.Fatalf("decision series = %d, want only the %d documented ones", got, len(flatten()))
	}
	at := time.Unix(1_800_000_000, 0)
	m.Pass(WorkerScheduledReleases, at)
	m.Pass("invented", at)
	if got := testutil.ToFloat64(m.lastPass.WithLabelValues(WorkerScheduledReleases)); got != float64(at.Unix()) {
		t.Fatalf("last pass = %v", got)
	}
	if got := testutil.CollectAndCount(m.lastPass); got != 1 {
		t.Fatalf("last pass series = %d, want 1", got)
	}
	m.Error(WorkerDatabaseSwitches)
	if got := testutil.ToFloat64(m.errors.WithLabelValues(WorkerDatabaseSwitches)); got != 1 {
		t.Fatalf("errors = %v", got)
	}
	var none *Metrics
	none.Pass(WorkerScheduledReleases, at)
	none.Error(WorkerScheduledReleases)
	none.Decision(GuardFreeze, "refused")
}

func flatten() []string {
	var out []string
	for guard, list := range outcomes {
		for _, outcome := range list {
			out = append(out, guard+"/"+outcome)
		}
	}
	return out
}
