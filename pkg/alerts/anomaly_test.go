package alerts_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// anomalyProm answers the ADR-744 queries: request_count is recognisable by
// increase(); anything else is the rule's metric. "offset Nd" queries are the
// baseline days; days beyond trafficDays had no traffic.
type anomalyProm struct {
	currentRequests, currentValue float64
	pastRequests, pastValue       float64
	trafficDays                   int
	offsetQueries                 atomic.Int32
}

func (p *anomalyProm) promQL() *selectivePromQL {
	return &selectivePromQL{fn: func(q string) (float64, error) {
		isRequests := strings.Contains(q, "increase(gateway_request_duration_seconds_count")
		if !strings.Contains(q, " offset ") {
			if isRequests {
				return p.currentRequests, nil
			}
			return p.currentValue, nil
		}
		p.offsetQueries.Add(1)
		for day := p.trafficDays + 1; day <= 7; day++ {
			if strings.Contains(q, " offset "+string(rune('0'+day))+"d") {
				return 0, nil // no traffic that day
			}
		}
		if isRequests {
			return p.pastRequests, nil
		}
		return p.pastValue, nil
	}}
}

// TestAnomalyRuleFiresAboveBaseline is the capability acceptance test
// (pkg/productcap/catalog.json, ADR-744).
func TestAnomalyRuleFiresAboveBaseline(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	prom := &anomalyProm{currentRequests: 400, currentValue: 5, pastRequests: 400, pastValue: 1, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, prom.promQL(), ident, dispatch)

	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatalf("stats=%+v calls=%d, want one fire for 5%% against a usual 1%% at 3x", stats, dispatch.callCount())
	}
	payload := dispatch.calls[0].Payload
	if payload["baseline"] != 1.0 || payload["baseline_days"] != 7 || payload["ratio"] != 5.0 {
		t.Fatalf("payload baseline fields = %v/%v/%v", payload["baseline"], payload["baseline_days"], payload["ratio"])
	}
	got, _ := store.AlertRuleByID(context.Background(), rule.ID)
	if got.State != state.AlertStateFiring {
		t.Fatalf("state = %q, want firing", got.State)
	}

	// The baseline is cached: a second tick within the hour re-reads only
	// the current window.
	before := prom.offsetQueries.Load()
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if after := prom.offsetQueries.Load(); after != before {
		t.Fatalf("baseline recomputed within the cache TTL: %d -> %d offset queries", before, after)
	}
}

func TestAnomalyRuleStaysQuietOnTinyNumbers(t *testing.T) {
	for _, tc := range []struct {
		name string
		prom *anomalyProm
	}{
		// 0.5% is 5x a 0.1% baseline but below the 1% error-rate floor.
		{"below absolute floor", &anomalyProm{currentRequests: 400, currentValue: 0.5, pastRequests: 400, pastValue: 0.1, trafficDays: 7}},
		// 10 requests are too few for a rate to mean anything.
		{"below minimum volume", &anomalyProm{currentRequests: 10, currentValue: 30, pastRequests: 400, pastValue: 1, trafficDays: 7}},
		// A zero baseline never makes "x times usual" fire.
		{"zero baseline", &anomalyProm{currentRequests: 400, currentValue: 5, pastRequests: 400, pastValue: 0, trafficDays: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
			ev, _ := makeEvaluator(t, store, tc.prom.promQL(), ident, dispatch)
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.Fired != 0 || dispatch.callCount() != 0 {
				t.Fatalf("stats=%+v: must not fire", stats)
			}
		})
	}
}

func TestAnomalyRuleNeedsEnoughHistory(t *testing.T) {
	store := state.NewMemStore()
	_, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	prom := &anomalyProm{currentRequests: 400, currentValue: 50, pastRequests: 400, pastValue: 1, trafficDays: 3}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	ev, _ := makeEvaluator(t, store, prom.promQL(), ident, dispatch)
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 0 || stats.SkippedInsufficient != 1 {
		t.Fatalf("stats=%+v, want skipped as insufficient with 3 of 7 days", stats)
	}
}

func TestAnomalyRuleBelowBaselineCatchesTrafficDrop(t *testing.T) {
	store := state.NewMemStore()
	_, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertBelowBaseline, 0.3)
	prom := &anomalyProm{currentRequests: 100, pastRequests: 1000, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, prom.promQL(), ident, dispatch)
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fired != 1 {
		t.Fatalf("stats=%+v, want a fire for 100 requests against a usual 1000 at 0.3x", stats)
	}
}
