package gateway

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
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

// adr: 20261104120000 — the per-stage series are bounded: past the per-app
// cap a new stage is not counted per stage, while stages already admitted
// keep counting.
func TestEnvironmentHostMetricsCap(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < api.GatewayStageMetricEnvironmentsPerApp+3; i++ {
		m.ObserveEnvironmentColdBoot("app-1", fmt.Sprintf("env-%d", i))
	}
	if n := testutil.CollectAndCount(m.environmentColdBoot); n != api.GatewayStageMetricEnvironmentsPerApp {
		t.Fatalf("series = %d, want %d", n, api.GatewayStageMetricEnvironmentsPerApp)
	}
	m.ObserveEnvironmentColdBoot("app-1", "env-0")
	if v := testutil.ToFloat64(m.environmentColdBoot.WithLabelValues("app-1", "env-0")); v != 2 {
		t.Fatalf("admitted stage boots = %v, want 2", v)
	}
	m.ObserveEnvironmentColdBoot("app-2", "env-x")
	if v := testutil.ToFloat64(m.environmentColdBoot.WithLabelValues("app-2", "env-x")); v != 1 {
		t.Fatalf("another app's stage = %v, want 1", v)
	}

	var s stageMetricEnvironments
	for i := 0; i < api.GatewayStageMetricEnvironmentsMax; i++ {
		if !s.admit(fmt.Sprintf("app-%d", i), "env") {
			t.Fatalf("pair %d refused under the total cap", i)
		}
	}
	if s.admit("one-more", "env") {
		t.Fatal("pair admitted past the total cap")
	}
}
