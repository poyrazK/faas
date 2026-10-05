package wire

import (
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestEventDeliveryMetricsScopesAndRecovery(t *testing.T) {
	m := NewOpsMetrics("sched")
	m.ObserveEventDeliveryCapacityDeferral("consumer")
	m.ObserveEventDeliveryCapacityDeferral("consumer")
	m.ObserveEventDeliveryCapacityDeferral("arbitrary-account-label")
	m.SetEventRoutingHealth(3, 40)
	if got := testutil.ToFloat64(m.eventDelivery.deferrals.WithLabelValues("consumer")); got != 2 {
		t.Fatalf("deferrals=%v", got)
	}
	if got := testutil.CollectAndCount(m.eventDelivery.deferrals); got != 3 {
		t.Fatalf("scope cardinality=%d", got)
	}
	if got := testutil.ToFloat64(m.eventDelivery.waiting); got != 3 {
		t.Fatalf("waiting=%v", got)
	}
	if got := testutil.ToFloat64(m.eventDelivery.oldest); got != 40 {
		t.Fatalf("age=%v", got)
	}
	m.SetEventRoutingHealth(0, 0)
	if testutil.ToFloat64(m.eventDelivery.waiting) != 0 || testutil.ToFloat64(m.eventDelivery.oldest) != 0 {
		t.Fatal("recovery left stale gauges")
	}
	var absent *OpsMetrics
	absent.ObserveEventDeliveryCapacityDeferral("consumer")
	absent.SetEventRoutingHealth(0, 0)
}
