package chaos

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestCorrelationSlotsAndHashDomains(t *testing.T) {
	run, scope := strings.Repeat("a", 32), strings.Repeat("b", 16)
	rule := Rule{From: "app", To: "dependency", Kind: KindHTTPStatus, StatusCode: 503, Percent: 100, CorrelationScope: scope}
	if err := (Plan{DurationMS: 1000, Rules: []Rule{rule}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []int{1, MaxRequestCorrelationSlots} {
		id := RequestCorrelationID(run, scope, slot)
		got, ok := RequestCorrelationSlot(run, rule, id)
		if !ok || got != slot {
			t.Fatal("valid slot rejected")
		}
	}
	for _, token := range []string{"", run + "." + scope + ".0", run + "." + scope + ".01", run + "." + scope + ".1025", RequestCorrelationID(strings.Repeat("c", 32), scope, 1), RequestCorrelationID(run, strings.Repeat("c", 16), 1)} {
		if _, ok := RequestCorrelationSlot(run, rule, token); ok {
			t.Fatal("invalid or foreign token accepted")
		}
	}
	ids := map[string]bool{}
	for _, id := range []string{RuleID(rule), TCPAttemptID(rule), CorrelationSupportID(rule), UncorrelatedAttemptID(rule), CorrelationCounterID(rule, 1), CorrelationCounterID(rule, 2)} {
		if len(id) != 64 || ids[id] {
			t.Fatal("counter hash domains collided")
		}
		ids[id] = true
	}
	before := Rule{From: "app", To: "dependency", Kind: KindHTTPStatus, StatusCode: 503, Percent: 100}
	raw, _ := json.Marshal(before)
	if strings.Contains(string(raw), "correlation_scope") {
		t.Fatal("legacy rule encoding changed")
	}
	for _, alter := range []func(*Rule){func(r *Rule) { r.Percent = 50 }, func(r *Rule) { r.From = "" }, func(r *Rule) { r.CorrelationScope = "bad" }, func(r *Rule) { r.Kind = KindTCPReset; r.Port = 6379 }} {
		invalid := rule
		alter(&invalid)
		if (Plan{DurationMS: 1000, Rules: []Rule{invalid}}).Validate() == nil {
			t.Fatal("unsafe correlation plan accepted")
		}
	}
}
