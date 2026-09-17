package main

import "testing"

func TestEdgeRuleConvergenceFenceMatchesPatternsAndGeneration(t *testing.T) {
	g := newGatewaydEdgeRules(nil, nil, nil, nil)
	g.BeginConvergence([]string{"*.Example.COM", "api.example.net"}, 7)
	if !g.Converging("one.example.com") {
		t.Fatal("wildcard hostname was not fenced")
	}
	if !g.Converging("API.EXAMPLE.NET") {
		t.Fatal("exact hostname comparison was not case-insensitive")
	}
	if g.Converging("unrelated.example.org") {
		t.Fatal("unrelated hostname was fenced")
	}

	// A delayed completion from an older generation cannot release a newer
	// fence for the same host.
	g.BeginConvergence([]string{"api.example.net"}, 8)
	g.EndConvergence([]string{"api.example.net"}, 7)
	if !g.Converging("api.example.net") {
		t.Fatal("older generation released a newer fence")
	}
	g.EndConvergence([]string{"api.example.net"}, 8)
	if g.Converging("api.example.net") {
		t.Fatal("current generation did not release its fence")
	}

	g.SetLoadedGeneration(12)
	g.SetLoadedGeneration(9)
	if got := g.loadedGeneration.Load(); got != 12 {
		t.Fatalf("loaded generation regressed to %d", got)
	}
}
