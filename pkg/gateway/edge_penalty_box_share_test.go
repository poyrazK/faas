package gateway

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022105846 amendment 1 — every box a Hit makes is reported once,
// with its client identity and end, and nothing else is.
func TestPenaltyBoxReportsBoxes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := penaltyBoxAt(&now)
	var got []PenaltyBoxRecord
	b.OnBox(func(rec PenaltyBoxRecord) { got = append(got, rec) })
	p := api.EdgeRulePenalty{Seconds: 600, After: 2, WithinSeconds: 60}
	for range 2 {
		b.Hit("app-1", "rule-1", net.ParseIP("2001:db8:1:2::77"), p)
	}
	want := PenaltyBoxRecord{AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("2001:db8:1:2::/64"), Until: now.Add(600 * time.Second)}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("reported %+v, want one %+v", got, want)
	}
}

// adr: 20261022105846 amendment 1 — a box another gateway made holds here:
// it keeps the later end of one already held, and an ended box or a client
// that is not a box identity is ignored.
func TestPenaltyBoxHoldsSharedBoxes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := penaltyBoxAt(&now)
	client := netip.MustParsePrefix("203.0.113.7/32")
	if !b.Hold(PenaltyBoxRecord{AppID: "app-1", RuleID: "rule-1", Client: client, Until: now.Add(10 * time.Minute)}) {
		t.Fatal("shared box refused")
	}
	b.Hold(PenaltyBoxRecord{AppID: "app-1", RuleID: "rule-2", Client: client, Until: now.Add(5 * time.Minute)})
	rule, left, ok := b.Boxed("app-1", net.ParseIP("203.0.113.7"))
	if !ok || rule != "rule-1" || left != 10*time.Minute {
		t.Fatalf("boxed = %q %v %v, want rule-1 for the later 10m end", rule, left, ok)
	}
	if _, _, ok := b.Boxed("app-2", net.ParseIP("203.0.113.7")); ok {
		t.Fatal("box held for another app")
	}
	for name, rec := range map[string]PenaltyBoxRecord{
		"ended":        {AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("198.51.100.1/32"), Until: now},
		"wide v4":      {AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("198.51.100.0/24"), Until: now.Add(time.Minute)},
		"v6 host":      {AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("2001:db8::1/128"), Until: now.Add(time.Minute)},
		"invalid":      {AppID: "app-1", RuleID: "rule-1", Until: now.Add(time.Minute)},
		"unmasked v6":  {AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("2001:db8::1/64"), Until: now.Add(time.Minute)},
		"mapped v4/32": {AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("::ffff:198.51.100.1/128"), Until: now.Add(time.Minute)},
	} {
		if b.Hold(rec) {
			t.Errorf("%s: held %+v", name, rec)
		}
	}
}
