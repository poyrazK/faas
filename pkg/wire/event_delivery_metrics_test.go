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

// A routing pass that fails every claim still completes, so routing results
// are counted on their own; the label set stays closed.
func TestEventRoutingOutcomesStayClosed(t *testing.T) {
	m := NewOpsMetrics("schedd")
	m.ObserveEventRoutingOutcome(EventRoutingClaimError)
	m.ObserveEventRoutingOutcome(EventRoutingClaimError)
	m.ObserveEventRoutingOutcome(EventRoutingRouted)
	m.ObserveEventRoutingOutcome("arbitrary-outcome")
	if got := testutil.ToFloat64(m.eventDelivery.outcomes.WithLabelValues(EventRoutingClaimError)); got != 2 {
		t.Fatalf("claim errors=%v", got)
	}
	if got := testutil.CollectAndCount(m.eventDelivery.outcomes); got != 5 {
		t.Fatalf("outcome cardinality=%d, want the five closed outcomes", got)
	}
	var absent *OpsMetrics
	absent.ObserveEventRoutingOutcome(EventRoutingRouted)
}
