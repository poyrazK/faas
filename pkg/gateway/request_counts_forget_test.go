// adr: 20261104091530 — a deleted app's request counters leave schedd's scrape.
package gateway

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestForgetAppRequestCountsDropsOnlyThatApp(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.ObserveRequest("gone", "pro", "200")
	m.ObserveRequest("gone", "pro", "404")
	m.ObserveRequest("kept", "pro", "200")
	m.AdjustAppInflight("gone", 1)

	m.ForgetAppRequestCounts("gone")

	if got := testutil.CollectAndCount(m.requests); got != 1 {
		t.Fatalf("request series after forget = %d, want 1 (kept)", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("kept", "pro", "200")); got != 1 {
		t.Fatalf("kept app counter = %v, want 1", got)
	}
	// The in-flight gauge stays so a finishing request cannot drive it negative.
	if got := testutil.ToFloat64(m.appInflight.WithLabelValues("gone")); got != 1 {
		t.Fatalf("deleted app in-flight gauge = %v, want 1", got)
	}
	var nilMetrics *Metrics
	nilMetrics.ForgetAppRequestCounts("gone")
}
