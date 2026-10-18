package livequery

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// adr: 20261016151947 — aggregates stay exact across column type changes that alter
// how values print, interleaved with writes and restarts, including changes
// made while the engine is down. The types are all scaled, so equal values
// always print alike (see oracleSchema.schemaChange).
func TestSchemaChangesAcrossRestarts(t *testing.T) {
	h := newHarness(t, `CREATE TABLE t0 (id bigint PRIMARY KEY, a int NOT NULL, b int, s text, n numeric)`)
	h.engine.Checkpointed = true
	h.register("q", `SELECT a % 2 AS g, (b IS NULL) AS h, count(*) AS c, min(n) AS mn, max(n) AS mx, avg(n) AS av, sum(n) AS sm
		FROM t0 GROUP BY 1, 2 HAVING count(*) > 1`)
	h.register("r", `SELECT id, n FROM t0 WHERE a > 2`)
	h.round()
	r := rand.New(rand.NewPCG(7, 7))
	nums := []string{"-3", "1.5", "0.125", "2.25", "7", "10.000"}
	write := func() {
		switch r.IntN(4) {
		case 0, 1:
			mustExec(t, h.pool, `INSERT INTO t0 VALUES ($1, $2, $3, 'x', $4::numeric) ON CONFLICT (id) DO UPDATE SET n = EXCLUDED.n`,
				1+r.IntN(20), r.IntN(6), []any{nil, 1}[r.IntN(2)], nums[r.IntN(len(nums))])
		case 2:
			mustExec(t, h.pool, `UPDATE t0 SET n = $1::numeric WHERE id = $2`, nums[r.IntN(len(nums))], 1+r.IntN(20))
		default:
			mustExec(t, h.pool, `DELETE FROM t0 WHERE id = $1`, 1+r.IntN(20))
		}
	}
	ddl := func() {
		mustExec(t, h.pool, fmt.Sprintf(`ALTER TABLE t0 ALTER COLUMN n TYPE %s`, []string{"numeric(20,4)", "numeric(20,2)", "numeric(10,1)"}[r.IntN(3)]))
	}
	for range 300 {
		switch k := r.IntN(10); {
		case k < 5:
			write()
		case k < 6:
			ddl()
		case k < 8:
			h.round()
		default:
			h.restartWith(r.IntN(5) == 0, func() {
				for range r.IntN(3) {
					if r.IntN(2) == 0 {
						ddl()
					} else {
						write()
					}
				}
			})
		}
	}
	h.round()
	t.Logf("stats %+v, %d restarts", h.engine.Stats, h.restarts)
}
