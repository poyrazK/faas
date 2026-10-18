package livequery

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

// TestRoundLatencyIdle measures rounds on a quiet database, where nothing
// but the round's own marker flushes WAL. It runs only with
// LIVEQUERY_BENCH=1.
func TestRoundLatencyIdle(t *testing.T) {
	if os.Getenv("LIVEQUERY_BENCH") == "" {
		t.Skip("set LIVEQUERY_BENCH=1 to run the benchmark")
	}
	h := newHarness(t, chatSchema)
	h.engine.VerifyEachRound = false
	h.register("feed", `SELECT id, body FROM messages WHERE channel_id = $1`, int64(1))
	h.round()
	var idle, write []time.Duration
	for i := range 40 {
		if i%2 == 1 {
			mustExec(t, h.pool, `INSERT INTO messages VALUES ($1, 1, 7, 'x')`, i)
		}
		start := time.Now()
		if _, err := h.engine.Round(context.Background()); err != nil {
			t.Fatal(err)
		}
		if i%2 == 1 {
			write = append(write, time.Since(start))
		} else {
			idle = append(idle, time.Since(start))
		}
	}
	slices.Sort(idle)
	slices.Sort(write)
	t.Logf("idle rounds p50 %v max %v; rounds after a write p50 %v max %v",
		idle[len(idle)/2], idle[len(idle)-1], write[len(write)/2], write[len(write)-1])
}
