package livequery

import "testing"

// adr: 1384 — a FULL JOIN followed by a LEFT JOIN over a self-joined table:
// NULL-extended rows must vanish when the nullable side gains a match.
func TestOuterJoinNullExtendedRowsFollowMatches(t *testing.T) {
	h := newHarness(t, `
		CREATE TABLE c0 (k1 int, k2 text, a int NOT NULL, b int, s text, ref bigint, PRIMARY KEY (k1, k2));
		CREATE TABLE t0 (id bigint PRIMARY KEY, a int NOT NULL, b int, s text, ref bigint);`)
	h.register("q", `SELECT (z.b IS NULL) AS g1, count(*) AS n, sum(y.a) AS sa
		FROM c0 x FULL JOIN t0 y ON y.ref = x.k1 LEFT JOIN c0 z ON z.ref = x.k1 GROUP BY 1`)
	h.register("rows", `SELECT x.k1, y.id, z.k2 FROM c0 x FULL JOIN t0 y ON y.ref = x.k1 LEFT JOIN c0 z ON z.ref = x.k1`)
	h.round()
	for _, step := range []string{
		`INSERT INTO t0 VALUES (38, 1, NULL, 'a', 16)`,
		`INSERT INTO c0 VALUES (16, 'q', 1, NULL, 'a', NULL)`,
		`INSERT INTO c0 VALUES (5, 'p', 1, NULL, 'a', 16)`,
		`UPDATE c0 SET k2 = 'p' WHERE k1 = 16`,
		`DELETE FROM c0 WHERE k1 = 16`,
		`UPDATE t0 SET ref = 5 WHERE id = 38`,
		`UPDATE c0 SET ref = 5 WHERE k1 = 5`,
		`DELETE FROM c0`,
	} {
		mustExec(t, h.pool, step)
		h.round()
	}
}
