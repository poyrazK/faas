package livequery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
)

// memoryStage reports the Go heap and the process's resident memory.
func memoryStage(t *testing.T, name string) {
	t.Helper()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rss := "?"
	if out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output(); err == nil {
		if kb, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
			rss = fmt.Sprintf("%.1f MiB", float64(kb)/1024)
		}
	}
	t.Logf("%-28s heap in use %6.1f MiB, from OS %6.1f MiB, RSS %s", name,
		float64(ms.HeapInuse)/(1<<20), float64(ms.Sys)/(1<<20), rss)
}

// TestEngineMemory measures what the engine costs at each stage: the
// parser's runtime, one subscription, and many. It runs only with
// LIVEQUERY_PROFILE set to a directory, where it writes a heap profile.
func TestEngineMemory(t *testing.T) {
	dir := os.Getenv("LIVEQUERY_PROFILE")
	if dir == "" {
		t.Skip("set LIVEQUERY_PROFILE=<dir> to run the memory profile")
	}
	memoryStage(t, "start")
	if _, err := parseSelect("SELECT 1"); err != nil {
		t.Fatal(err)
	}
	memoryStage(t, "after the first parse")
	for i := range 200 {
		if _, err := parseSelect(fmt.Sprintf("SELECT id, body FROM messages WHERE channel_id = %d ORDER BY id DESC LIMIT 10", i)); err != nil {
			t.Fatal(err)
		}
	}
	memoryStage(t, "after 200 parses")

	h := newHarness(t, chatSchema+`;
		INSERT INTO channels SELECT g, 'c' || g, false FROM generate_series(1, 200) g;
		INSERT INTO messages SELECT g, 1 + g % 200, 7, 'body ' || g FROM generate_series(1, 10000) g;`)
	h.engine.VerifyEachRound = false
	memoryStage(t, "harness ready")
	ctx := context.Background()
	// Rounds run on the engine directly: the harness's clients would hold
	// copies of every result and count against the measurement.
	round := func() {
		t.Helper()
		if _, err := h.engine.Round(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.engine.Register(ctx, "one", `SELECT id, body FROM messages WHERE channel_id = $1`, int64(1)); err != nil {
		t.Fatal(err)
	}
	round()
	memoryStage(t, "one subscription")
	for i := range 2000 {
		// 200 distinct queries (shapes x tables), 2,000 subscriptions.
		sql := []string{
			`SELECT id, body FROM messages WHERE channel_id = $1`,
			`SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 20`,
			`SELECT channel_id, count(*) AS n FROM messages WHERE channel_id = $1 GROUP BY channel_id`,
			`SELECT c.name, m.body FROM channels c JOIN messages m ON m.channel_id = c.id WHERE c.id = $1`,
		}[i%4] + fmt.Sprintf(" /* variant %d */", i%50)
		if _, err := h.engine.Register(ctx, fmt.Sprint("s", i), sql, int64(1+i%200)); err != nil {
			t.Fatal(err)
		}
	}
	round()
	memoryStage(t, "200 queries, 2,000 subs")
	for range 5 {
		mustExec(t, h.pool, `UPDATE messages SET body = body || '.' WHERE id % 97 = 0`)
		round()
	}
	memoryStage(t, "after five write rounds")
	f, err := os.Create(dir + "/heap.pprof")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Fatal(err)
	}
	t.Logf("engine: %d queries, %d subscriptions", h.engine.Queries(), h.engine.Subscriptions())
}
