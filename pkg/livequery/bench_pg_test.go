package livequery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const benchSchema = `
	CREATE TABLE customers (id bigint PRIMARY KEY, name text NOT NULL, region text NOT NULL);
	CREATE TABLE orders (id bigint PRIMARY KEY, customer_id bigint NOT NULL, status text NOT NULL,
		amount numeric(12,2) NOT NULL, created_at timestamptz NOT NULL);
	CREATE INDEX orders_customer ON orders (customer_id, created_at);
	CREATE INDEX orders_created ON orders (created_at);
	INSERT INTO customers SELECT g, 'c' || g, 'r' || (g % 8) FROM generate_series(1, 10000) g;
	INSERT INTO orders SELECT g, 1 + (g % 10000), CASE WHEN g % 3 = 0 THEN 'open' ELSE 'done' END,
		(g % 1000) / 7.0, timestamptz '2026-01-01' + g * interval '1 minute' FROM generate_series(1, 100000) g;
	ANALYZE customers; ANALYZE orders;`

func percentile(xs []time.Duration, p float64) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)]
}

// TestBenchLiveQueries measures commit-to-delta latency and per-round work
// under a steady write load. It runs only with LIVEQUERY_BENCH=1.
func TestBenchLiveQueries(t *testing.T) {
	if os.Getenv("LIVEQUERY_BENCH") == "" {
		t.Skip("set LIVEQUERY_BENCH=1 to run the benchmark")
	}
	subs := envInt("LIVEQUERY_BENCH_SUBS", 100)
	tps := envInt("LIVEQUERY_BENCH_TPS", 200)
	seconds := envInt("LIVEQUERY_BENCH_SECONDS", 15)
	h := newHarness(t, benchSchema)
	h.engine.VerifyEachRound = os.Getenv("LIVEQUERY_BENCH_VERIFY") != ""
	ctx := context.Background()
	for i := 1; i <= subs; i++ {
		h.register(fmt.Sprintf("customer%d", i), `
			SELECT o.id, o.status, o.amount, c.name FROM orders o JOIN customers c ON c.id = o.customer_id
			WHERE o.customer_id = $1`, int64(i))
	}
	// LIVEQUERY_BENCH_FEED adds a latest-10 feed per subscriber: "window"
	// maintains it incrementally; "recompute" writes it with a LEFT JOIN,
	// which returns the same rows but falls back to recompute.
	if feed := os.Getenv("LIVEQUERY_BENCH_FEED"); feed != "" {
		join := map[string]string{"window": "JOIN", "recompute": "LEFT JOIN"}[feed]
		for i := 1; i <= subs; i++ {
			p := h.register(fmt.Sprintf("feed%d", i), `
				SELECT o.id, o.amount, c.name FROM orders o `+join+` customers c ON c.id = o.customer_id
				WHERE o.customer_id = $1 ORDER BY o.created_at DESC LIMIT 10`, int64(1+i%200))
			if i == 1 {
				t.Logf("feed: mode %s %s window=%v", p.Mode, p.Reason, p.Order != nil)
			}
		}
	}
	extra := map[string]string{
		"regions": `SELECT c.region, count(*) AS n, sum(o.amount) AS total FROM orders o JOIN customers c ON c.id = o.customer_id
			WHERE o.status = 'open' GROUP BY c.region`,
		"idle_customers": `SELECT c.id, c.name FROM customers c LEFT JOIN orders o ON o.customer_id = c.id WHERE o.id IS NULL`,
		"latest":         `SELECT id, amount FROM orders ORDER BY created_at DESC LIMIT 20`,
		"count_all":      `SELECT count(*) FROM orders`,
	}
	for _, id := range []string{"regions", "idle_customers", "latest", "count_all"} {
		if os.Getenv("LIVEQUERY_BENCH_SKIP_"+id) != "" {
			continue
		}
		p := h.register(id, extra[id])
		t.Logf("%s: mode %s %s", id, p.Mode, p.Reason)
	}
	start := time.Now()
	h.round()
	t.Logf("initial round: %v for %d queries", time.Since(start), len(h.engine.byID))

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	var nextID atomic.Int64
	nextID.Store(1_000_000)
	var committed atomic.Int64
	var wg sync.WaitGroup
	writers := 4
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(w), 7))
			tick := time.NewTicker(time.Second * time.Duration(writers) / time.Duration(tps))
			defer tick.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-tick.C:
				}
				var err error
				switch n := r.IntN(100); {
				case n < 60:
					_, err = h.pool.Exec(runCtx, `UPDATE orders SET status = $1, amount = amount + 1 WHERE id = $2`,
						pickOne(r, []string{"open", "done"}), 1+r.IntN(100000))
				case n < 85:
					_, err = h.pool.Exec(runCtx, `INSERT INTO orders VALUES ($1, $2, 'open', 10, now())`,
						nextID.Add(1), 1+r.IntN(10000))
				case n < 95:
					_, err = h.pool.Exec(runCtx, `DELETE FROM orders WHERE id = $1`, 1+r.IntN(100000))
				default:
					_, err = h.pool.Exec(runCtx, `UPDATE customers SET name = name || '.' WHERE id = $1`, 1+r.IntN(10000))
				}
				if err == nil {
					committed.Add(1)
				}
			}
		}(w)
	}

	type stmtStat struct {
		n       int
		total   time.Duration
		members int
	}
	stmts := map[string]*stmtStat{}
	h.engine.StatementHook = func(sql string, members int, d time.Duration) {
		st := stmts[sql]
		if st == nil {
			st = &stmtStat{}
			stmts[sql] = st
		}
		st.n++
		st.total += d
		st.members = max(st.members, members)
	}
	var latencies, rounds []time.Duration
	var applied, statements int
	before := h.engine.Stats.Statements
	beforeTime := h.engine.Stats.StatementTime
	benchStart := time.Now()
	for runCtx.Err() == nil {
		roundStart := time.Now()
		h.round()
		done := time.Now()
		rounds = append(rounds, done.Sub(roundStart))
		for _, txn := range h.engine.LastApplied {
			latencies = append(latencies, done.Sub(txn.CommitTime))
		}
		applied += len(h.engine.LastApplied)
	}
	elapsed := time.Since(benchStart)
	wg.Wait()
	statements = h.engine.Stats.Statements - before
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("load: %d subscriptions + %d other queries, %d writers at %d tps target, %v",
		subs, len(h.engine.byID)-subs, writers, tps, elapsed.Round(time.Millisecond))
	t.Logf("throughput: %d committed, %d applied (%.0f/s)", committed.Load(), applied, float64(applied)/elapsed.Seconds())
	t.Logf("rounds: %d, duration p50 %v p95 %v max %v, %.1f statements/round",
		len(rounds), percentile(rounds, 0.5), percentile(rounds, 0.95), percentile(rounds, 1), float64(statements)/float64(len(rounds)))
	var roundTotal time.Duration
	for _, r := range rounds {
		roundTotal += r
	}
	t.Logf("time in maintenance statements: %v of %v total round time",
		(h.engine.Stats.StatementTime - beforeTime).Round(time.Millisecond), roundTotal.Round(time.Millisecond))
	t.Logf("commit-to-delta: p50 %v p95 %v p99 %v max %v",
		percentile(latencies, 0.5), percentile(latencies, 0.95), percentile(latencies, 0.99), percentile(latencies, 1))
	t.Logf("engine heap %d MiB, stats %+v", mem.HeapAlloc>>20, h.engine.Stats)
	keys := make([]string, 0, len(stmts))
	for k := range stmts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return int(stmts[b].total - stmts[a].total) })
	for _, k := range keys[:min(6, len(keys))] {
		st := stmts[k]
		t.Logf("statement %v total over %d runs (%d members, avg %v): %.220s", st.total.Round(time.Millisecond), st.n, st.members, (st.total / time.Duration(st.n)).Round(time.Microsecond), k)
	}
}
