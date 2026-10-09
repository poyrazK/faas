package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeEdgeRuleListStore struct {
	fakeEdgeRuleStore
	lists []state.EdgeRuleList
	err   error
	asked map[string][]string
}

func (f *fakeEdgeRuleListStore) EdgeRuleListsByName(_ context.Context, accountID string, names []string) ([]state.EdgeRuleList, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.asked == nil {
		f.asked = map[string][]string{}
	}
	f.asked[accountID] = names
	var out []state.EdgeRuleList
	for _, l := range f.lists {
		for _, n := range names {
			if l.AccountID == accountID && l.Name == n {
				out = append(out, l)
			}
		}
	}
	return out, nil
}

func listRule(id, account, list string) state.EdgeRule {
	return state.EdgeRule{ID: id, AccountID: account, AppID: "app-" + account,
		Match: &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: list}}
}

// ADR-907: lists are resolved per account at host load, and a reference
// that cannot be resolved disables only the rule that makes it.
func TestResolveEdgeRuleLists(t *testing.T) {
	store := &fakeEdgeRuleListStore{lists: []state.EdgeRuleList{
		{AccountID: "a", Name: "office", Kind: api.EdgeRuleListKindIP, Items: []string{"203.0.113.0/24"}},
		{AccountID: "b", Name: "office", Kind: api.EdgeRuleListKindIP, Items: []string{"198.51.100.0/24"}},
	}}
	g := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	rules := []state.EdgeRule{
		listRule("r1", "a", "office"),
		listRule("r2", "b", "office"),
		listRule("r3", "a", "missing"),
		{ID: "r4", AccountID: "a"},
	}
	got, err := g.resolveEdgeRuleLists(context.Background(), rules)
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].MatchLists != nil {
		t.Fatal("input rules mutated")
	}
	if want := []string{"missing", "office"}; len(store.asked["a"]) != 2 || store.asked["a"][0] != want[0] {
		t.Fatalf("asked a for %v, want %v", store.asked["a"], want)
	}
	matches := func(r state.EdgeRule, ip string) bool {
		cond := compileEdgeRuleCondition(r.ID, r.AppID, r.Mode, r.Match, r.MatchLists)
		return cond.Match.Matches(api.EdgeRuleMatchInput{ClientIP: net.ParseIP(ip)})
	}
	if !matches(got[0], "203.0.113.5") || matches(got[0], "198.51.100.5") {
		t.Fatal("account a's rule must use account a's list")
	}
	if !matches(got[1], "198.51.100.5") || matches(got[1], "203.0.113.5") {
		t.Fatal("account b's rule must use account b's list")
	}
	if matches(got[2], "203.0.113.5") {
		t.Fatal("a rule referencing a missing list must never match")
	}
	if got[3].MatchLists != nil {
		t.Fatal("unconditioned rule got lists")
	}

	store.err = errors.New("db down")
	if _, err := g.resolveEdgeRuleLists(context.Background(), rules); err == nil {
		t.Fatal("store error must fail the load")
	}
}

func TestResolveEdgeRuleListsWithoutListStore(t *testing.T) {
	g := newGatewaydEdgeRules(&fakeEdgeRuleStore{}, newQuietLogger(), nil, nil)
	rules := []state.EdgeRule{listRule("r1", "a", "office")}
	got, err := g.resolveEdgeRuleLists(context.Background(), rules)
	if err != nil || got[0].MatchLists != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	cond := compileEdgeRuleCondition("r1", "app", "", got[0].Match, got[0].MatchLists)
	if cond.Match.Matches(api.EdgeRuleMatchInput{ClientIP: net.ParseIP("203.0.113.5")}) {
		t.Fatal("unresolved list reference must never match")
	}
}
