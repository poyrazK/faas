package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 6): every host load read and
// compiled the lists its rules reference, so one 10,000-item list under a "*"
// rule was held once per cached host and read from Postgres on every host's
// load. Host loads now share one compiled list per account until an
// invalidation of that account drops it.
func TestEdgeRuleListsAreSharedAcrossHostLoads(t *testing.T) {
	store := &fakeEdgeRuleListStore{lists: []state.EdgeRuleList{
		{AccountID: "a", Name: "office", Kind: api.EdgeRuleListKindIP, Items: []string{"203.0.113.0/24"}},
	}}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	rules := []state.EdgeRule{listRule("r1", "a", "office")}
	ctx := context.Background()

	first, err := g.resolveEdgeRuleLists(ctx, rules)
	if err != nil {
		t.Fatal(err)
	}
	store.asked = nil
	second, err := g.resolveEdgeRuleLists(ctx, rules)
	if err != nil {
		t.Fatal(err)
	}
	if store.asked != nil {
		t.Fatalf("a second host load read the lists again: %v", store.asked)
	}
	if first[0].MatchLists["office"] != second[0].MatchLists["office"] {
		t.Fatal("host loads compiled their own copies of one list")
	}

	g.InvalidateAccountHosts("a", "", []string{"*"})
	store.lists[0].Items = []string{"198.51.100.0/24"}
	third, err := g.resolveEdgeRuleLists(ctx, rules)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.asked["a"]; len(got) != 1 || got[0] != "office" {
		t.Fatalf("after the account's invalidation the load asked %v, want [office]", got)
	}
	if third[0].MatchLists["office"] == first[0].MatchLists["office"] {
		t.Fatal("an invalidation must drop the account's compiled lists")
	}
}
