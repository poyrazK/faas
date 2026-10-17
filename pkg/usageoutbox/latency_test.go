package usageoutbox

import (
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

// TestEnqueueLatencyProfile measures how long a durable Enqueue takes on the
// disk under FAAS_USAGE_OUTBOX_LATENCY_DIR. ADR-1505 puts this wait in front
// of the last bytes of every monetized response, so its percentiles are the
// latency that change adds. Skipped unless the directory is set.
func TestEnqueueLatencyProfile(t *testing.T) {
	dir := os.Getenv("FAAS_USAGE_OUTBOX_LATENCY_DIR")
	if dir == "" {
		t.Skip("set FAAS_USAGE_OUTBOX_LATENCY_DIR to a directory on the disk to measure")
	}
	for _, concurrency := range []int{1, 8, 32, 128} {
		root, err := os.MkdirTemp(dir, "outbox-latency-")
		if err != nil {
			t.Fatal(err)
		}
		q, err := Open(root, DefaultMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		const perWorker = 200
		latencies := make([]time.Duration, 0, concurrency*perWorker)
		var mu sync.Mutex
		var wg sync.WaitGroup
		started := time.Now()
		for w := range concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				local := make([]time.Duration, 0, perWorker)
				for i := range perWorker {
					event := Event{EventID: fmt.Sprintf("00000000-0000-4000-8000-%06d%06d", w, i), AccountID: "a", AppID: "b",
						ConsumerID: "c", WindowStart: time.Now().UTC().Truncate(time.Minute), RequestCount: 1, BillableUnits: 1}
					at := time.Now()
					if err := q.Enqueue(event); err != nil {
						t.Error(err)
						return
					}
					local = append(local, time.Since(at))
				}
				mu.Lock()
				latencies = append(latencies, local...)
				mu.Unlock()
			}()
		}
		wg.Wait()
		elapsed := time.Since(started)
		slices.Sort(latencies)
		pct := func(p float64) time.Duration { return latencies[int(p*float64(len(latencies)-1))] }
		t.Logf("concurrency=%d events=%d throughput=%.0f/s p50=%s p95=%s p99=%s max=%s", concurrency, len(latencies),
			float64(len(latencies))/elapsed.Seconds(), pct(0.50), pct(0.95), pct(0.99), latencies[len(latencies)-1])
		_ = os.RemoveAll(root)
	}
}
