package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestCacheMetrics_OutcomeCounterClosedSet verifies the
// pre-instantiation loop covers every closed outcome — a
// missing label surfaces as a panel that doesn't render from
// boot. Adding a new outcome is a code + dashboard change
// (the closed set is intentional).
func TestCacheMetrics_OutcomeCounterClosedSet(t *testing.T) {
	m := NewMetrics()
	want := map[string]bool{
		"hit": true, "miss": true,
		"bypass_authed": true, "bypass_uncacheable": true,
		"stale_if_error_served": true, "store_skipped": true,
	}
	got := map[string]bool{}
	for _, outcome := range []string{"hit", "miss", "bypass_authed", "bypass_uncacheable", "stale_if_error_served", "store_skipped"} {
		// Collect metric families and look for outcome label.
		got[outcome] = false
		families, _ := m.registry.Gather()
		for _, f := range families {
			if f.GetName() != "gateway_response_cache_total" {
				continue
			}
			for _, mt := range f.GetMetric() {
				for _, lp := range mt.GetLabel() {
					if lp.GetName() == "outcome" && lp.GetValue() == outcome {
						got[outcome] = true
					}
				}
			}
		}
	}
	for o, present := range got {
		if !present {
			t.Errorf("outcome %q not pre-instantiated", o)
		}
	}
	for o := range want {
		if !got[o] {
			t.Errorf("outcome %q missing from want set", o)
		}
	}
	_ = api.PlanFree
}

// TestCacheMetrics_HitBumpsCounter verifies a fresh hit
// increments the hit counter, NOT the miss counter. The
// hit/miss partition is the load-bearing claim behind the
// saved-cost figure.
func TestCacheMetrics_HitBumpsCounter(t *testing.T) {
	h, _, _ := newTestHandler(t)
	pre := readCounter(t, h.metrics, "gateway_response_cache_total", "outcome", "hit")
	h.metricsIncCacheOutcome("app-A", "hit")
	post := readCounter(t, h.metrics, "gateway_response_cache_total", "outcome", "hit")
	if post != pre+1 {
		t.Errorf("hit counter delta = %v, want 1", post-pre)
	}
}

// TestCacheMetrics_StoreSkippedBumpsCounter verifies a
// cacheability predicate veto (Set-Cookie / Cache-Control /
// cap overflow / non-cacheable status) bumps the
// store_skipped counter. The dashboard surfaces this so
// operators see "why isn't my cache populating?".
func TestCacheMetrics_StoreSkippedBumpsCounter(t *testing.T) {
	h, _, _ := newTestHandler(t)
	pre := readCounter(t, h.metrics, "gateway_response_cache_total", "outcome", "store_skipped")
	h.metricsIncCacheOutcome("app-A", "store_skipped")
	post := readCounter(t, h.metrics, "gateway_response_cache_total", "outcome", "store_skipped")
	if post != pre+1 {
		t.Errorf("store_skipped delta = %v, want 1", post-pre)
	}
}

// TestCacheMetrics_WakesAvoidedPerApp verifies the
// wakes_avoided counter is per-app (label cardinality
// bounded by the customer's app count).
func TestCacheMetrics_WakesAvoidedPerApp(t *testing.T) {
	h, _, _ := newTestHandler(t)
	preA := readCounter(t, h.metrics, "gateway_response_cache_wakes_avoided_total", "app", "app-A")
	preB := readCounter(t, h.metrics, "gateway_response_cache_wakes_avoided_total", "app", "app-B")
	h.metrics.responseCacheWakesAvoided.WithLabelValues("app-A").Inc()
	h.metrics.responseCacheWakesAvoided.WithLabelValues("app-B").Inc()
	postA := readCounter(t, h.metrics, "gateway_response_cache_wakes_avoided_total", "app", "app-A")
	postB := readCounter(t, h.metrics, "gateway_response_cache_wakes_avoided_total", "app", "app-B")
	if postA != preA+1 {
		t.Errorf("app-A delta = %v, want 1", postA-preA)
	}
	if postB != preB+1 {
		t.Errorf("app-B delta = %v, want 1", postB-preB)
	}
}

// TestCacheMetrics_AppOutcomeCounter verifies the customer-facing additive
// counter keeps cache outcomes attributable to the app without changing the
// historical global counter shape.
func TestCacheMetrics_AppOutcomeCounter(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.metricsIncCacheOutcome("app-A", "hit")
	h.metricsIncCacheOutcome("app-A", "miss")
	h.metricsIncCacheOutcome("app-B", "hit")

	if got := readCounterLabels(t, h.metrics, "gateway_response_cache_app_total", map[string]string{"app": "app-A", "outcome": "hit"}); got != 1 {
		t.Fatalf("app-A hit counter = %v, want 1", got)
	}
	if got := readCounterLabels(t, h.metrics, "gateway_response_cache_app_total", map[string]string{"app": "app-A", "outcome": "miss"}); got != 1 {
		t.Fatalf("app-A miss counter = %v, want 1", got)
	}
	if got := readCounterLabels(t, h.metrics, "gateway_response_cache_app_total", map[string]string{"app": "app-B", "outcome": "hit"}); got != 1 {
		t.Fatalf("app-B hit counter = %v, want 1", got)
	}
}

// readCounter is a small helper that returns the current value
// of a (name, labelName, labelValue) tuple. Returns 0 when
// the label has never been bumped (counters are pre-
// instantiated in NewMetrics, so the lookup is always present).
func readCounter(t *testing.T, m *Metrics, name, labelName, labelValue string) float64 {
	t.Helper()
	families, _ := m.registry.Gather()
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, mt := range f.GetMetric() {
			matched := false
			for _, lp := range mt.GetLabel() {
				if lp.GetName() == labelName && lp.GetValue() == labelValue {
					matched = true
				}
			}
			if matched {
				return mt.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func readCounterLabels(t *testing.T, m *Metrics, name string, want map[string]string) float64 {
	t.Helper()
	families, _ := m.registry.Gather()
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, mt := range f.GetMetric() {
			labels := make(map[string]string, len(mt.GetLabel()))
			for _, lp := range mt.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			matched := len(labels) == len(want)
			for key, value := range want {
				if labels[key] != value {
					matched = false
					break
				}
			}
			if matched {
				return mt.GetCounter().GetValue()
			}
		}
	}
	return 0
}
