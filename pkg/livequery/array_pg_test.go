package livequery

import "testing"

// adr: 20261016151947 — array parameters and array sort keys survive batching: unnest
// flattens an array of arrays, so they travel as text and are cast where
// they are used.
func TestArrayParametersAndSortKeys(t *testing.T) {
	h := newHarness(t, `CREATE TABLE posts (id bigint PRIMARY KEY, channel_id bigint NOT NULL, tags int[])`)
	mustExec(t, h.pool, `INSERT INTO posts VALUES (1, 1, '{3}'), (2, 2, '{1,2}'), (3, 3, NULL)`)
	h.register("in12", `SELECT id FROM posts WHERE channel_id = ANY($1)`, "{1,2}")
	h.register("in3", `SELECT id FROM posts WHERE channel_id = ANY($1)`, "{3}")
	h.register("recompute", `SELECT channel_id, count(*) FROM posts WHERE channel_id = ANY($1) GROUP BY channel_id HAVING count(*) > 0 ORDER BY channel_id LIMIT 5`, "{1,2,3}")
	h.register("bytags", `SELECT id, tags FROM posts ORDER BY tags DESC NULLS LAST LIMIT 2`)
	h.round()
	if got := h.ids("in12") + "|" + h.ids("in3") + "|" + h.ids("bytags"); got != "1,2,|3,|1,2," {
		t.Fatalf("initial %s", got)
	}
	mustExec(t, h.pool, `INSERT INTO posts VALUES (4, 1, '{9}'); UPDATE posts SET channel_id = 3 WHERE id = 2`)
	h.round()
	if got := h.ids("in12") + "|" + h.ids("in3") + "|" + h.ids("bytags"); got != "1,4,|2,3,|1,4," {
		t.Fatalf("after writes %s", got)
	}
}
