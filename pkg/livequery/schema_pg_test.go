package livequery

import (
	"sort"
	"strings"
	"testing"
)

// outcome summarises a round's delta for one subscription: "-" for none,
// "reset", "closed" or "changed".
func outcomes(deltas []Delta) string {
	var parts []string
	for _, d := range deltas {
		kind := "-"
		switch {
		case d.Closed != nil:
			kind = "closed:" + d.Closed.Code
		case d.Reset:
			kind = "reset"
		case !d.Empty():
			kind = "changed"
		}
		parts = append(parts, d.Query+"="+kind)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// adr: 1384 — schema changes reach live queries in the round where they
// commit: untouched queries are left alone, touched ones are recompiled and
// diffed, a changed result shape sends a full result, and a query that no
// longer compiles is closed with schema_changed.
func TestSchemaChanges(t *testing.T) {
	h := newHarness(t, chatSchema)
	h.register("all", `SELECT * FROM messages WHERE channel_id = $1`, int64(1))
	h.register("bodies", `SELECT m.id, m.body, c.name FROM messages m JOIN channels c ON c.id = m.channel_id`)
	h.register("counts", `SELECT channel_id, count(*) AS n, min(body) AS first FROM messages GROUP BY channel_id`)
	h.register("feed", `SELECT id, body FROM messages ORDER BY id DESC LIMIT 2`)
	h.register("names", `SELECT id, name FROM channels`)
	h.round()
	mustExec(t, h.pool, `INSERT INTO channels VALUES (1, 'general', false), (2, 'random', false)`)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (1, 1, 7, 'a'), (2, 1, 7, 'b'), (3, 2, 8, 'c')`)
	h.round()
	for _, step := range []struct {
		sql        string
		want       string
		recompiles int
	}{
		{`CREATE INDEX messages_body ON messages (body)`,
			"all=- bodies=- counts=- feed=- names=-", 0},
		{`ALTER TABLE messages ADD COLUMN edited boolean NOT NULL DEFAULT false; INSERT INTO messages VALUES (4, 1, 7, 'd')`,
			"all=reset bodies=changed counts=changed feed=changed names=-", 4},
		{`ALTER TABLE messages ALTER COLUMN body TYPE varchar(20)`,
			"all=reset bodies=reset counts=- feed=reset names=-", 4},
		{`UPDATE messages SET body = 'x' WHERE id = 1`,
			"all=changed bodies=changed counts=changed feed=- names=-", 0},
		{`ALTER TABLE channels RENAME COLUMN name TO title`,
			"all=- bodies=closed:schema_changed counts=- feed=- names=closed:schema_changed", 2},
		{`INSERT INTO messages VALUES (5, 2, 8, 'e')`,
			"all=- counts=changed feed=changed", 0},
		{`DROP TABLE messages`,
			"all=closed:schema_changed counts=closed:schema_changed feed=closed:schema_changed", 3},
	} {
		before := h.engine.Stats.Recompiles
		mustExec(t, h.pool, step.sql)
		if got := outcomes(h.round()); got != step.want {
			t.Fatalf("%s:\n got %s\nwant %s", step.sql, got, step.want)
		}
		if got := h.engine.Stats.Recompiles - before; got != step.recompiles {
			t.Fatalf("%s: %d recompiles, want %d", step.sql, got, step.recompiles)
		}
	}
	if n := len(h.engine.byID); n != 0 {
		t.Fatalf("%d subscriptions left after every query closed", n)
	}
	// A new subscription after the changes compiles against the new schema.
	h.register("titles", `SELECT id, title FROM channels`)
	if got := outcomes(h.round()); got != "titles=reset" {
		t.Fatalf("after re-subscribing: %s", got)
	}
}
