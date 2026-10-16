package livequery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The differential oracle (ADR-1384 Phase 0). Random schemas, queries and
// concurrent write workloads; after every round each maintained result must
// equal the query executed at the round snapshot (Engine.VerifyEachRound),
// and every simulated client must equal the engine.

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// oracleSchema describes the generated tables. Every table has the columns
// a (int not null), b (int), s (text) and ref (bigint); "t" tables have a
// bigint id key, "c" tables a composite (k1 int, k2 text) key.
type oracleSchema struct {
	tables    []string
	composite map[string]bool
}

func (s oracleSchema) ddl() string {
	var b strings.Builder
	for _, t := range s.tables {
		if s.composite[t] {
			fmt.Fprintf(&b, "CREATE TABLE %s (k1 int, k2 text, a int NOT NULL, b int, s text, ref bigint, n numeric, PRIMARY KEY (k1, k2));\n", t)
		} else {
			fmt.Fprintf(&b, "CREATE TABLE %s (id bigint PRIMARY KEY, a int NOT NULL, b int, s text, ref bigint, n numeric);\n", t)
		}
	}
	return b.String()
}

func randomSchema(r *rand.Rand) oracleSchema {
	s := oracleSchema{composite: map[string]bool{}}
	for i := range 2 + r.IntN(2) {
		s.tables = append(s.tables, fmt.Sprintf("t%d", i))
	}
	s.tables = append(s.tables, "c0")
	s.composite["c0"] = true
	return s
}

// joinKey returns an expression identifying a row of alias for join
// conditions: id for t tables, k1 for c tables.
func (s oracleSchema) idOf(table, alias string) string {
	if s.composite[table] {
		return alias + ".k1"
	}
	return alias + ".id"
}

type oracleQuery struct {
	sql  string
	args []any
}

func pickOne[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// fromClause builds a 1-3 table FROM clause and returns the aliases used.
func (s oracleSchema) fromClause(r *rand.Rand) (string, []string, map[string]string) {
	return s.fromJoins(r, []string{"JOIN", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN"})
}

func (s oracleSchema) fromJoins(r *rand.Rand, joins []string) (string, []string, map[string]string) {
	tables := map[string]string{}
	x := pickOne(r, s.tables)
	tables["x"] = x
	from := fmt.Sprintf("%s x", x)
	aliases := []string{"x"}
	n := 1 + r.IntN(3)
	for i := 1; i < n; i++ {
		alias := []string{"", "y", "z"}[i]
		t := pickOne(r, s.tables)
		tables[alias] = t
		target := pickOne(r, aliases)
		cond := fmt.Sprintf("%s.ref = %s", alias, s.idOf(tables[target], target))
		if r.IntN(4) == 0 {
			cond += fmt.Sprintf(" AND %s.a %% 2 = 0", alias)
		}
		from += fmt.Sprintf(" %s %s %s ON %s", pickOne(r, joins), t, alias, cond)
		aliases = append(aliases, alias)
	}
	return from, aliases, tables
}

func (s oracleSchema) predicate(r *rand.Rand, aliases []string, tables map[string]string, params *[]any) string {
	return s.predicateOf(r, aliases, tables, params, 12)
}

// predicateOf draws from the first kinds predicate kinds; kinds 7 and above
// are subqueries.
func (s oracleSchema) predicateOf(r *rand.Rand, aliases []string, tables map[string]string, params *[]any, kinds int) string {
	var preds []string
	for range r.IntN(3) {
		a := pickOne(r, aliases)
		u := pickOne(r, s.tables)
		uid := "q.id"
		if s.composite[u] {
			uid = "q.k1"
		}
		switch r.IntN(kinds) {
		case 7:
			preds = append(preds, fmt.Sprintf("%s.ref IN (SELECT %s FROM %s q WHERE q.a %% 2 = 0)", a, uid, u))
		case 8:
			preds = append(preds, fmt.Sprintf("EXISTS (SELECT 1 FROM %s q WHERE q.ref = %s)", u, s.idOf(tables[a], a)))
		case 9:
			preds = append(preds, fmt.Sprintf("NOT EXISTS (SELECT 1 FROM %s q WHERE q.ref = %s AND q.b IS NOT NULL)", u, s.idOf(tables[a], a)))
		case 10:
			preds = append(preds, fmt.Sprintf("(%s.a > 6 OR EXISTS (SELECT 1 FROM %s q WHERE q.ref = %s AND q.s IS NOT NULL))", a, u, s.idOf(tables[a], a)))
		case 11:
			preds = append(preds, fmt.Sprintf("%s.a NOT IN (SELECT q.a FROM %s q WHERE q.b IS NOT NULL)", a, u))
		case 0:
			*params = append(*params, int32(r.IntN(8)))
			preds = append(preds, fmt.Sprintf("%s.a > $%d", a, len(*params)))
		case 1:
			preds = append(preds, fmt.Sprintf("%s.b IS NULL", a))
		case 2:
			preds = append(preds, fmt.Sprintf("%s IS NULL", s.idOf(tables[a], a)))
		case 3:
			preds = append(preds, fmt.Sprintf("%s.a %% 3 <> 1", a))
		case 4:
			preds = append(preds, fmt.Sprintf("coalesce(%s.s, '') LIKE 'a%%'", a))
		case 5:
			preds = append(preds, fmt.Sprintf("(%s.b IS NOT NULL OR %s.a > 5)", a, pickOne(r, aliases)))
		case 6:
			preds = append(preds, fmt.Sprintf("%s IS NOT NULL", s.idOf(tables[a], a)))
		}
	}
	if len(preds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(preds, " AND ")
}

func (s oracleSchema) randomQuery(r *rand.Rand) oracleQuery {
	if r.IntN(100) < envInt("LIVEQUERY_ORACLE_WINDOW_PERCENT", 17) {
		return s.windowQuery(r)
	}
	from, aliases, tables := s.fromClause(r)
	var params []any
	where := s.predicate(r, aliases, tables, &params)
	switch r.IntN(10) {
	case 0, 1, 2, 3:
		var cols []string
		for i, a := range aliases {
			cols = append(cols, fmt.Sprintf("%s.a AS a%d", a, i), fmt.Sprintf("%s.s AS s%d", a, i))
		}
		cols = append(cols, fmt.Sprintf("coalesce(%s.b, 0) + %s.a AS e", pickOne(r, aliases), pickOne(r, aliases)))
		return oracleQuery{sql: "SELECT " + strings.Join(cols, ", ") + " FROM " + from + where, args: params}
	case 4, 5, 6, 7:
		a, b := pickOne(r, aliases), pickOne(r, aliases)
		groups := []string{
			a + ".a % 4", b + ".s", "(" + b + ".b IS NULL)", s.idOf(tables[a], a), "coalesce(" + b + ".a, -1) % 3",
		}
		g1 := pickOne(r, groups)
		sel := "SELECT " + g1 + " AS g1"
		groupBy := " GROUP BY 1"
		if r.IntN(2) == 0 {
			sel += ", " + pickOne(r, groups) + " AS g2"
			groupBy = " GROUP BY 1, 2"
		}
		sel += ", " + s.aggregates(r, a, b)
		if r.IntN(3) == 0 {
			sel += fmt.Sprintf(", string_agg(coalesce(%s.s, '~'), ',' ORDER BY coalesce(%s.s, '~')) AS agg", b, b)
		}
		having := ""
		if r.IntN(3) == 0 {
			having = " HAVING count(*) > 1"
		}
		return oracleQuery{sql: sel + " FROM " + from + where + groupBy + having, args: params}
	case 8:
		switch r.IntN(3) {
		case 0:
			return oracleQuery{sql: fmt.Sprintf("SELECT %s.a, %s.s FROM %s%s ORDER BY 1 DESC NULLS LAST, 2 NULLS FIRST LIMIT 4", aliases[0], aliases[0], from, where), args: params}
		case 1:
			a, b := pickOne(r, aliases), pickOne(r, aliases)
			having := ""
			if r.IntN(2) == 0 {
				params = append(params, int64(r.IntN(6)))
				having = fmt.Sprintf(" HAVING count(*) > $%d", len(params))
			}
			return oracleQuery{sql: "SELECT " + s.aggregates(r, a, b) + " FROM " + from + where + having, args: params}
		default:
			a, b := pickOne(r, aliases), pickOne(r, aliases)
			return oracleQuery{sql: fmt.Sprintf("SELECT DISTINCT %s.a %% 3 AS d1, %s.s AS d2 FROM %s%s", a, b, from, where), args: params}
		}
	default:
		t := pickOne(r, s.tables)
		return oracleQuery{sql: fmt.Sprintf("SELECT count(*) AS n, sum(a) AS sa FROM %s WHERE ref IN (SELECT a FROM %s)", t, pickOne(r, s.tables))}
	}
}

// windowQuery returns an ORDER BY ... LIMIT query over inner joins: sort
// items with ties, both directions and NULL orders, a collation, positions
// and output names, sort keys left out of the select list, OFFSET, and
// limits that are constants, parameters, NULL or absent.
func (s oracleSchema) windowQuery(r *rand.Rand) oracleQuery {
	from, aliases, tables := s.fromJoins(r, []string{"JOIN"})
	var params []any
	where := s.predicateOf(r, aliases, tables, &params, 7)
	a := pickOne(r, aliases)
	cols := []string{fmt.Sprintf("%s.s AS s0", a)}
	if r.IntN(3) > 0 {
		cols = append(cols, fmt.Sprintf("%s.a AS a0", pickOne(r, aliases)), fmt.Sprintf("coalesce(%s.b, 0) + %s.a AS e", pickOne(r, aliases), a))
	}
	var items []string
	for range r.IntN(4) {
		b := pickOne(r, aliases)
		item := pickOne(r, []string{
			b + ".a", b + ".s", b + ".n", b + ".b", "(" + b + ".a % 3)", b + `.s COLLATE "C"`, "coalesce(" + b + ".s, 'zz')", "1",
		})
		if len(cols) > 1 && r.IntN(5) == 0 {
			item = pickOne(r, []string{"e", "a0", "2"})
		}
		item += pickOne(r, []string{"", " ASC", " DESC"}) + pickOne(r, []string{"", "", " NULLS FIRST", " NULLS LAST"})
		items = append(items, item)
	}
	sql := "SELECT " + strings.Join(cols, ", ") + " FROM " + from + where
	if len(items) > 0 {
		sql += " ORDER BY " + strings.Join(items, ", ")
	}
	switch r.IntN(6) {
	case 0:
		if len(items) > 0 {
			break
		}
		sql += " LIMIT 3"
	case 1:
		params = append(params, pickOne(r, []any{int64(2), int64(5), nil}))
		sql += fmt.Sprintf(" LIMIT $%d", len(params))
	case 2:
		sql += " LIMIT ALL"
	default:
		sql += fmt.Sprintf(" LIMIT %d", 1+r.IntN(6))
	}
	switch r.IntN(4) {
	case 0:
		sql += fmt.Sprintf(" OFFSET %d", r.IntN(4))
	case 1:
		params = append(params, int64(r.IntN(3)))
		sql += fmt.Sprintf(" OFFSET $%d", len(params))
	}
	return oracleQuery{sql: sql, args: params}
}

// aggregates returns a random list of incrementally maintainable aggregates.
func (s oracleSchema) aggregates(r *rand.Rand, a, b string) string {
	all := []string{
		"count(*) AS n",
		fmt.Sprintf("count(%s.a) AS nb", b),
		fmt.Sprintf("sum(%s.a) AS sa", a),
		fmt.Sprintf("sum(%s.n) AS sn", b),
		fmt.Sprintf("avg(%s.a) AS aa", a),
		fmt.Sprintf("avg(%s.n) AS an", b),
		fmt.Sprintf("min(%s.s) AS mn", b),
		fmt.Sprintf("max(%s.s) AS mxs", a),
		fmt.Sprintf("min(%s.n) AS mnn", a),
		fmt.Sprintf("max(coalesce(%s.b, -1)) AS mx", b),
		fmt.Sprintf("sum(%s.a::numeric / 4) AS sq", a),
		fmt.Sprintf("count(%s.*) AS rows_%s", b, b),
	}
	var out []string
	for _, agg := range all {
		if r.IntN(2) == 0 {
			out = append(out, agg)
		}
	}
	if len(out) == 0 {
		out = append(out, all[0])
	}
	return strings.Join(out, ", ")
}

// writer runs random transactions until it has committed or abandoned n.
func (s oracleSchema) writer(ctx context.Context, pool *pgxpool.Pool, r *rand.Rand, n int) error {
	strs := []any{"a", "ab", "b", nil}
	for range n {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if r.IntN(5) == 0 {
			if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
				return err
			}
		}
		ok := true
		for range 1 + r.IntN(4) {
			t := pickOne(r, s.tables)
			stmt, args := s.statement(r, t, strs)
			if _, err := tx.Exec(ctx, stmt, args...); err != nil {
				ok = false
				break
			}
		}
		if !ok || r.IntN(12) == 0 {
			_ = tx.Rollback(ctx)
			continue
		}
		if err := tx.Commit(ctx); err != nil && ctx.Err() != nil {
			return err
		}
	}
	return nil
}

// schemaChange returns a schema change for t: columns added and dropped, column
// types that change how values print (numeric scale) or the result shape
// (text and varchar), and indexes that change nothing. n moves only between
// scaled types: back to unconstrained numeric, old rows would keep their
// scale while new ones print without it, and min or max over equal values
// that print differently returns whichever PostgreSQL meets last.
func (s oracleSchema) schemaChange(r *rand.Rand, t string) string {
	return pickOne(r, []string{
		"ALTER TABLE " + t + " ADD COLUMN IF NOT EXISTS x int",
		"ALTER TABLE " + t + " DROP COLUMN IF EXISTS x",
		"ALTER TABLE " + t + " ALTER COLUMN n TYPE numeric(20,4)",
		"ALTER TABLE " + t + " ALTER COLUMN n TYPE numeric(20,2)",
		"ALTER TABLE " + t + " ALTER COLUMN s TYPE varchar(40)",
		"ALTER TABLE " + t + " ALTER COLUMN s TYPE text",
		"CREATE INDEX IF NOT EXISTS " + t + "_a_idx ON " + t + " (a)",
		"DROP INDEX IF EXISTS " + t + "_a_idx",
	})
}

func (s oracleSchema) statement(r *rand.Rand, t string, strs []any) (string, []any) {
	if r.IntN(1000) < envInt("LIVEQUERY_ORACLE_DDL_PERMILLE", 5) {
		return s.schemaChange(r, t), nil
	}
	maybeNull := func(v int) any {
		if r.IntN(4) == 0 {
			return nil
		}
		return v
	}
	ref := maybeNull(1 + r.IntN(40))
	nums := []any{nil, "1.5", "2.25", "-3", "0.125", "7", "10.000"}
	if r.IntN(30) == 0 {
		nums = []any{"NaN", "Infinity", "-Infinity"}
	}
	num := pickOne(r, nums)
	if s.composite[t] {
		switch r.IntN(6) {
		case 0, 1:
			return fmt.Sprintf("INSERT INTO %s VALUES ($1, $2, $3, $4, $5, $6, $7::numeric) ON CONFLICT DO NOTHING", t),
				[]any{1 + r.IntN(20), pickOne(r, []string{"p", "q"}), r.IntN(10), maybeNull(r.IntN(10)), pickOne(r, strs), ref, num}
		case 2:
			return fmt.Sprintf("UPDATE %s SET k2 = CASE k2 WHEN 'p' THEN 'q' ELSE 'p' END WHERE k1 = $1", t), []any{1 + r.IntN(20)}
		case 3:
			return fmt.Sprintf("UPDATE %s SET a = $1, s = $2, n = $3::numeric WHERE k1 = $4", t), []any{r.IntN(10), pickOne(r, strs), num, 1 + r.IntN(20)}
		case 4:
			return fmt.Sprintf("UPDATE %s SET ref = $1 WHERE k1 = $2", t), []any{ref, 1 + r.IntN(20)}
		default:
			return fmt.Sprintf("DELETE FROM %s WHERE k1 = $1", t), []any{1 + r.IntN(20)}
		}
	}
	switch r.IntN(11) {
	case 0, 1, 2:
		return fmt.Sprintf("INSERT INTO %s VALUES ($1, $2, $3, $4, $5, $6::numeric) ON CONFLICT (id) DO NOTHING", t),
			[]any{1 + r.IntN(40), r.IntN(10), maybeNull(r.IntN(10)), pickOne(r, strs), ref, num}
	case 3:
		return fmt.Sprintf("UPDATE %s SET a = $1, b = $2, n = $3::numeric WHERE id = $4", t), []any{r.IntN(10), maybeNull(r.IntN(10)), num, 1 + r.IntN(40)}
	case 4:
		return fmt.Sprintf("UPDATE %s SET ref = $1 WHERE id = $2", t), []any{ref, 1 + r.IntN(40)}
	case 5:
		return fmt.Sprintf("UPDATE %s SET s = $1 WHERE a = $2", t), []any{pickOne(r, strs), r.IntN(10)}
	case 6:
		return fmt.Sprintf("UPDATE %s SET id = $1 WHERE id = $2", t), []any{1 + r.IntN(60), 1 + r.IntN(40)}
	case 7:
		return fmt.Sprintf("DELETE FROM %s WHERE id = $1", t), []any{1 + r.IntN(40)}
	case 8:
		return fmt.Sprintf("DELETE FROM %s WHERE a = $1", t), []any{r.IntN(10)}
	case 9:
		if r.IntN(40) == 0 {
			return fmt.Sprintf("TRUNCATE %s", t), nil
		}
		return fmt.Sprintf("UPDATE %s SET a = a + 1 WHERE ref = $1", t), []any{1 + r.IntN(40)}
	default:
		return fmt.Sprintf("UPDATE %s SET b = NULL WHERE id %% 3 = $1", t), []any{r.IntN(3)}
	}
}

// adr: 1384 — Phase 0 exit gate: zero divergences between maintained results
// and the query at each round snapshot, under concurrent writes.
func TestOracleRandomWorkloads(t *testing.T) {
	if testing.Short() {
		t.Skip("oracle workloads are long")
	}
	seeds := envInt("LIVEQUERY_ORACLE_SEEDS", 3)
	queries := envInt("LIVEQUERY_ORACLE_QUERIES", 10)
	writers := envInt("LIVEQUERY_ORACLE_WRITERS", 4)
	txns := envInt("LIVEQUERY_ORACLE_TXNS", 120)
	base := uint64(time.Now().UnixNano())
	if v, err := strconv.ParseUint(os.Getenv("LIVEQUERY_ORACLE_BASE_SEED"), 10, 64); err == nil {
		base = v
	}
	var total Stats
	for i := range seeds {
		seed := base + uint64(i)
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			stats := runOracle(t, seed, queries, writers, txns)
			total.Rounds += stats.Rounds
			total.Applied += stats.Applied
			total.Held += stats.Held
			total.Keyed += stats.Keyed
			total.Incremental += stats.Incremental
			total.Rebuilds += stats.Rebuilds
			total.Verified += stats.Verified
			total.ExpandedKeys += stats.ExpandedKeys
			total.WindowRanks += stats.WindowRanks
			total.WindowRefills += stats.WindowRefills
			total.Recompiles += stats.Recompiles
			total.Skipped += stats.Skipped
		})
	}
	t.Logf("oracle totals: %+v", total)
	mixed := envInt("LIVEQUERY_ORACLE_WINDOW_PERCENT", 17) < 100
	if total.Keyed == 0 || total.WindowRanks == 0 || mixed && (total.ExpandedKeys == 0 || total.Incremental == 0) {
		t.Fatalf("incremental paths not exercised: %+v", total)
	}
}

func runOracle(t *testing.T, seed uint64, nqueries, nwriters, ntxns int) Stats {
	r := rand.New(rand.NewPCG(seed, seed^0x5eed))
	schema := randomSchema(r)
	h := newHarness(t, schema.ddl())
	h.engine.Checkpointed = true
	ctx := context.Background()
	// Seed data so the first queries have something to join.
	if err := schema.writer(ctx, h.pool, rand.New(rand.NewPCG(seed, 1)), 60); err != nil {
		t.Fatal(err)
	}
	registered := 0
	for attempts := 0; registered < nqueries && attempts < nqueries*5; attempts++ {
		q := schema.randomQuery(r)
		if _, err := h.engine.Register(ctx, fmt.Sprintf("q%d", registered), q.sql, q.args...); err != nil {
			t.Logf("skip query (%v): %s", err, q.sql)
			continue
		}
		h.clients[fmt.Sprintf("q%d", registered)] = &client{}
		t.Logf("q%d %v: %s", registered, q.args, q.sql)
		registered++
	}
	// Exercise plan groups: more members of each parameterised query, and a
	// duplicate subscription that shares a member.
	for _, g := range slices.Clone(h.engine.groups) {
		m := g.members[0]
		dup := fmt.Sprintf("%s-dup", m.ids[0])
		if _, err := h.engine.Register(ctx, dup, g.plan.SQL, m.args...); err != nil {
			t.Fatal(err)
		}
		h.clients[dup] = &client{}
		for v := range 2 {
			if g.plan.NParams == 0 {
				break
			}
			args := make([]any, g.plan.NParams)
			for k := range args {
				args[k] = int32(r.IntN(8))
			}
			id := fmt.Sprintf("%s-m%d", m.ids[0], v)
			if _, err := h.engine.Register(ctx, id, g.plan.SQL, args...); err != nil {
				t.Fatal(err)
			}
			h.clients[id] = &client{}
		}
	}
	modes := map[string]int{}
	for _, g := range h.engine.groups {
		modes[g.plan.Mode.String()] += len(g.members)
		if len(g.plan.Subqueries) > 0 {
			modes["keyed-subquery"] += len(g.members)
		}
		if g.plan.Agg != nil {
			modes["incremental-agg"] += len(g.members)
		}
		if g.plan.Order != nil {
			modes["window"] += len(g.members)
		}
	}
	t.Logf("seed %d: tables %v, queries by mode %v", seed, schema.tables, modes)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, nwriters)
	for w := range nwriters {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wr := rand.New(rand.NewPCG(seed, uint64(100+w)))
			if err := schema.writer(runCtx, h.pool, wr, ntxns); err != nil && runCtx.Err() == nil {
				errs <- err
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	// Restart the engine from a checkpoint now and then, sometimes losing
	// the slot, as parking a database and recovering from a crash do.
	rr := rand.New(rand.NewPCG(seed, 3))
	restartPercent := envInt("LIVEQUERY_ORACLE_RESTART_PERCENT", 8)
	for running := true; running; {
		select {
		case <-done:
			running = false
		case err := <-errs:
			t.Fatalf("writer: %v", err)
		default:
		}
		h.round()
		if rr.IntN(100) < restartPercent {
			h.restart(rr.IntN(4) == 0)
		}
	}
	// Drain: transactions committed after the last round's snapshot.
	for range 3 {
		h.round()
	}
	if len(h.engine.pending) != 0 {
		t.Fatalf("%d transactions still pending after the workload", len(h.engine.pending))
	}
	t.Logf("seed %d stats: %+v, %d restarts", seed, h.engine.Stats, h.restarts)
	return h.engine.Stats
}
