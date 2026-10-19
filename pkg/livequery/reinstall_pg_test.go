package livequery

import "testing"

// adr: 20261016151947 — installing the schema-change log again (every livequeryd start
// does) is not itself a schema change: no query is recompiled.
func TestReinstallIsQuiet(t *testing.T) {
	h := newHarness(t, chatSchema)
	h.register("all", `SELECT * FROM messages`)
	h.round()
	if err := Install(t.Context(), h.pool); err != nil {
		t.Fatal(err)
	}
	before := h.engine.Stats.Recompiles
	if got := outcomes(h.round()); got != "all=-" {
		t.Fatalf("after reinstalling: %s", got)
	}
	if n := h.engine.Stats.Recompiles - before; n != 0 {
		t.Fatalf("reinstalling recompiled %d queries", n)
	}
}
