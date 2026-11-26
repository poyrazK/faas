package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 20261104120000 — requests on an environment host are also counted per
// environment, so a stage's alert rules read the stage's own traffic; a
// request on a production host adds no environment series.
func TestEnvironmentHostMetrics(t *testing.T) {
	m := NewMetrics()
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withEnvironmentHostMark(context.Background()))
	if got := environmentHostFrom(r); got != "" {
		t.Fatalf("unmarked environment = %q", got)
	}
	markEnvironmentHost(r, "env-1")
	if got := environmentHostFrom(r); got != "env-1" {
		t.Fatalf("marked environment = %q, want env-1", got)
	}

	m.ObserveEnvironmentRequestDuration("app-1", environmentHostFrom(r), "5xx", 20*time.Millisecond)
	m.ObserveEnvironmentRequestDuration("app-1", "", "2xx", 20*time.Millisecond)
	m.ObserveEnvironmentColdBoot("app-1", "env-1")
	m.ObserveEnvironmentColdBoot("app-1", "")
	m.ObserveEnvironmentCapacityRefusal("app-1", "env-1")
	m.ObserveEnvironmentCapacityRefusal("app-1", "")

	if n := testutil.CollectAndCount(m.environmentRequestDuration); n != 1 {
		t.Fatalf("environment request series = %d, want 1", n)
	}
	if v := testutil.ToFloat64(m.environmentColdBoot.WithLabelValues("app-1", "env-1")); v != 1 {
		t.Fatalf("environment cold boots = %v, want 1", v)
	}
	if n := testutil.CollectAndCount(m.environmentColdBoot); n != 1 {
		t.Fatalf("environment cold boot series = %d, want 1", n)
	}
	if v := testutil.ToFloat64(m.environmentCapacityRefusals.WithLabelValues("app-1", "env-1")); v != 1 {
		t.Fatalf("environment capacity refusals = %v, want 1", v)
	}
	var nilMetrics *Metrics
	nilMetrics.ObserveEnvironmentRequestDuration("app-1", "env-1", "2xx", time.Millisecond)
	markEnvironmentHost(httptest.NewRequest("GET", "/", nil), "env-1") // no slot: ignored
}
