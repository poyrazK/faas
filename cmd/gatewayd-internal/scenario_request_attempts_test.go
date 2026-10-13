package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestScenarioRequestAttemptRecorderBoundsKeysAndRejectsForeignIDs(t *testing.T) {
	run, scope := strings.Repeat("a", 32), strings.Repeat("b", 16)
	rule := chaos.Rule{CorrelationScope: scope, From: "app", To: "dependency", Kind: chaos.KindHTTPStatus, StatusCode: 503, Percent: 100}
	recorder := &scenarioChaosMatchRecorder{pending: map[scenarioChaosMatchKey]int64{}}
	for slot := 1; slot <= chaos.MaxRequestCorrelationSlots; slot++ {
		recorder.ObserveRequestAttempt(run, "caller", "generation", rule, chaos.RequestCorrelationID(run, scope, slot))
	}
	for i := 0; i < 10000; i++ {
		recorder.ObserveRequestAttempt(run, "caller", "generation", rule, chaos.RequestCorrelationID(run, scope, chaos.MaxRequestCorrelationSlots+1+i))
	}
	recorder.ObserveRequestAttempt(run, "caller", "generation", rule, chaos.RequestCorrelationID(strings.Repeat("c", 32), scope, 1))
	if len(recorder.pending) != chaos.MaxRequestCorrelationSlots+2 {
		t.Fatalf("unbounded correlation keys: %d", len(recorder.pending))
	}
	for key, count := range recorder.pending {
		if key.ruleID == chaos.UncorrelatedAttemptID(rule) && count != 10001 {
			t.Fatal("invalid IDs not counted")
		}
		if key.ruleID == chaos.CorrelationSupportID(rule) && count != 11025 {
			t.Fatal("support coverage lost")
		}
	}
	before := len(recorder.pending)
	rule.CorrelationScope = ""
	recorder.ObserveRequestAttempt(run, "caller", "generation", rule, "unrelated")
	if len(recorder.pending) != before {
		t.Fatal("uncorrelated rules created counters")
	}
}
