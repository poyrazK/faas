package livequery

import (
	"slices"
	"strings"
	"testing"
)

// adr: 20261016151947 — a latest-N feed is maintained without re-reading its window:
// a new row enters and pushes the last one out, edits that keep the order
// update in place, rows past a full window stay out, and only a window that
// loses rows nothing replaces is read again. Every ordered delta carries the
// new order.
func TestOrderedFeed(t *testing.T) {
	h := newHarness(t, chatSchema)
	const feed = `SELECT m.id, m.body, c.name FROM messages m JOIN channels c ON c.id = m.channel_id
		WHERE m.channel_id = $1 ORDER BY m.id DESC LIMIT 3`
	plan := h.register("feed", feed, int64(1))
	h.register("other", feed, int64(2))
	if plan.Order == nil || plan.Reason != "" {
		t.Fatalf("feed compiled to %s (%s)", plan.Mode, plan.Reason)
	}
	h.round()
	mustExec(t, h.pool, `INSERT INTO channels VALUES (1, 'general', false), (2, 'random', false)`)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (1, 1, 7, 'a'), (2, 1, 7, 'b'), (3, 1, 7, 'c'), (4, 1, 7, 'd'), (5, 1, 7, 'e'), (9, 2, 7, 'z')`)
	h.round()
	ids := func() string {
		var out []string
		for _, r := range h.engine.Result("feed") {
			out = append(out, *r.Values[0])
		}
		return strings.Join(out, ",")
	}
	for _, step := range []struct {
		sql                      string
		ids                      string
		ranks, refills           int
		order, upserted, removed bool
	}{
		{`INSERT INTO messages VALUES (6, 1, 7, 'f')`, "6,5,4", 1, 0, true, true, true},
		{`UPDATE messages SET body = 'E' WHERE id = 5`, "6,5,4", 0, 0, false, true, false},
		{`UPDATE channels SET name = 'main' WHERE id = 1`, "6,5,4", 0, 0, false, true, false},
		{`INSERT INTO messages VALUES (0, 1, 7, 'old')`, "6,5,4", 1, 0, false, false, false},
		{`DELETE FROM messages WHERE id = 6`, "5,4,3", 1, 1, true, true, true},
		{`UPDATE messages SET id = 10 WHERE id = 3`, "10,5,4", 1, 0, true, true, true},
		// Removing rows keeps the others' order, so the delta needs none.
		{`DELETE FROM messages WHERE channel_id = 1 AND id < 5`, "10,5", 1, 1, false, false, true},
	} {
		before := h.engine.Stats
		mustExec(t, h.pool, step.sql)
		var d *Delta
		for _, delta := range h.round() {
			if delta.Empty() {
				continue
			}
			if delta.Query == "other" {
				t.Fatalf("%s: delta for an unaffected member: %+v", step.sql, delta)
			}
			d = &delta
		}
		if got := ids(); got != step.ids {
			t.Fatalf("%s: feed %s, want %s", step.sql, got, step.ids)
		}
		ranks, refills := h.engine.Stats.WindowRanks-before.WindowRanks, h.engine.Stats.WindowRefills-before.WindowRefills
		if ranks != step.ranks || refills != step.refills {
			t.Fatalf("%s: %d ranks and %d refills, want %d and %d", step.sql, ranks, refills, step.ranks, step.refills)
		}
		if !step.order && !step.upserted && !step.removed {
			if d != nil {
				t.Fatalf("%s: unexpected delta %+v", step.sql, *d)
			}
			continue
		}
		if d == nil {
			t.Fatalf("%s: no delta", step.sql)
		}
		if (d.Order != nil) != step.order || (len(d.Upserts) > 0) != step.upserted || (len(d.Removes) > 0) != step.removed {
			t.Fatalf("%s: delta order %v upserts %d removes %d", step.sql, d.Order, len(d.Upserts), len(d.Removes))
		}
		if d.Order != nil {
			var want []string
			for _, r := range h.engine.Result("feed") {
				want = append(want, r.ID)
			}
			if !slices.Equal(d.Order, want) {
				t.Fatalf("%s: delta order %v, result order %v", step.sql, d.Order, want)
			}
		}
	}
}
