package main

import "testing"

func TestEdgeRuleConvergenceFenceMatchesPatternsAndGeneration(t *testing.T) {
	g := newGatewaydEdgeRules(nil, nil, nil, nil)
	g.BeginConvergence("acct-a", []string{"*.Example.COM", "api.example.net"}, 7)
	if !g.Converging("one.example.com", "acct-a") {
		t.Fatal("wildcard hostname was not fenced")
	}
	if !g.Converging("API.EXAMPLE.NET", "acct-a") {
		t.Fatal("exact hostname comparison was not case-insensitive")
	}
	if g.Converging("unrelated.example.org", "acct-a") {
		t.Fatal("unrelated hostname was fenced")
	}

	// A delayed completion from an older generation cannot release a newer
	// fence for the same host.
	g.BeginConvergence("acct-a", []string{"api.example.net"}, 8)
	g.EndConvergence("acct-a", []string{"api.example.net"}, 7)
	if !g.Converging("api.example.net", "acct-a") {
		t.Fatal("older generation released a newer fence")
	}
	g.EndConvergence("acct-a", []string{"api.example.net"}, 8)
	if g.Converging("api.example.net", "acct-a") {
		t.Fatal("current generation did not release its fence")
	}

	g.SetLoadedGeneration(12)
	g.SetLoadedGeneration(9)
	if got := g.loadedGeneration.Load(); got != 12 {
		t.Fatalf("loaded generation regressed to %d", got)
	}
}

// A rule's match_host is free-form, so any account can write "*" or another
// tenant's hostname. Request-time matching ignores that account's rules on
// hosts it does not own, so its mutation must not 503 those hosts.
func TestEdgeRuleConvergenceFenceIsScopedToMutatingAccount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fenceAccount  string
		requestOwner  string
		wantConverged bool
	}{
		{"owner's own mutation fences its host", "acct-a", "acct-a", true},
		{"foreign wildcard mutation leaves an owned host serving", "acct-b", "acct-a", false},
		{"unclaimed host keeps the fleet-wide hold", "acct-b", "", true},
		{"unscoped legacy fence holds every owner", "", "acct-a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGatewaydEdgeRules(nil, nil, nil, nil)
			g.BeginConvergence(tc.fenceAccount, []string{"*"}, 3)
			if got := g.Converging("victim.apps.example.com", tc.requestOwner); got != tc.wantConverged {
				t.Fatalf("Converging = %v, want %v", got, tc.wantConverged)
			}
		})
	}

	// Two accounts fencing the same pattern release independently: one
	// account's apply does not lift the other's hold.
	g := newGatewaydEdgeRules(nil, nil, nil, nil)
	g.BeginConvergence("acct-a", []string{"shared.example.com"}, 4)
	g.BeginConvergence("acct-b", []string{"shared.example.com"}, 5)
	g.EndConvergence("acct-b", []string{"shared.example.com"}, 5)
	if !g.Converging("shared.example.com", "acct-a") {
		t.Fatal("another account's apply released this account's fence")
	}
}
