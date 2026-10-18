package livequery

import "testing"

// adr: 20261016151947 — a subscriber that joins a result the engine already maintains
// receives it whole, and a result that outgrows its limit is closed with
// live_query_limit.
func TestLateSubscribersAndLimits(t *testing.T) {
	h := newHarness(t, chatSchema)
	h.engine.MaxResultRows = 3
	h.register("first", `SELECT id, body FROM messages WHERE channel_id = $1`, int64(1))
	h.round()
	mustExec(t, h.pool, `INSERT INTO messages VALUES (1, 1, 7, 'a'), (2, 1, 7, 'b')`)
	h.round()
	h.register("late", `SELECT id, body FROM messages WHERE channel_id = $1`, int64(1))
	if got := outcomes(h.round()); got != "first=- late=reset" {
		t.Fatalf("late join: %s", got)
	}
	if n := len(h.clients["late"].result.Rows()); n != 2 {
		t.Fatalf("late subscriber holds %d rows, want 2", n)
	}
	mustExec(t, h.pool, `INSERT INTO messages VALUES (3, 1, 7, 'c'), (4, 1, 7, 'd')`)
	deltas := h.round()
	if got := outcomes(deltas); got != "first=closed:live_query_limit late=closed:live_query_limit" {
		t.Fatalf("over the limit: %s", got)
	}
	if c := deltas[0].Closed; c.Limit != 3 || c.Observed != 4 {
		t.Fatalf("limit error %+v", c)
	}
	if h.engine.Subscriptions() != 0 || h.engine.Queries() != 0 {
		t.Fatalf("%d subscriptions and %d queries left", h.engine.Subscriptions(), h.engine.Queries())
	}
	if d, ok := h.engine.Current("first"); ok {
		t.Fatalf("closed subscription still has a result: %+v", d)
	}
}
