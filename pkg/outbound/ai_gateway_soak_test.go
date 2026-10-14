package outbound

// adr: 998 — preview soak: concurrent streamed AI calls through the handler
// with a production-sized Postgres pool. Opt in with FAAS_AI_SOAK=1;
// FAAS_AI_SOAK_SECONDS, FAAS_AI_SOAK_WORKERS and FAAS_AI_SOAK_POOL tune it, and
// FAAS_AI_SOAK_CIRCUIT=1 turns on the integration's circuit breaker.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	soakChunks        = 20
	soakChunkInterval = 5 * time.Millisecond
	soakInputTokens   = 40
	soakOutputTokens  = 20
)

func soakEnvInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

// soakProvider streams an OpenAI chat completion in soakChunks events and a
// usage chunk, like a short generation.
func soakProvider(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for range soakChunks {
		_, _ = fmt.Fprint(w, "data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"tok \"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(soakChunkInterval)
	}
	_, _ = fmt.Fprintf(w, "data: {\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n",
		soakInputTokens, soakOutputTokens)
}

func TestAIGatewaySoak(t *testing.T) {
	if os.Getenv("FAAS_AI_SOAK") != "1" {
		t.Skip("set FAAS_AI_SOAK=1 to run the AI gateway soak")
	}
	duration := time.Duration(soakEnvInt("FAAS_AI_SOAK_SECONDS", 60)) * time.Second
	workers := soakEnvInt("FAAS_AI_SOAK_WORKERS", 32)
	poolSize := soakEnvInt("FAAS_AI_SOAK_POOL", 2) // outboundd's production pool (pkg/db)
	f := newAIPreviewFixture(t, soakProvider, state.OutboundAIPolicy{RequestLogEnabled: true})
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE accounts SET plan = 'scale' WHERE id = $1`, f.account.ID); err != nil {
		t.Fatal(err)
	}
	policy := api.OutboundRequestPolicy{RatePerSecond: 500, Burst: 2000, MaxInFlight: 1000, RequestTimeoutMS: 30_000}
	if os.Getenv("FAAS_AI_SOAK_CIRCUIT") == "1" {
		policy.CircuitBreakerFailureThreshold, policy.CircuitBreakerOpenSeconds = 5, 30
	}
	if err := f.store.SetOutboundRequestPolicy(ctx, f.account.ID, f.offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	pool, tracer := f.tracedPool(poolSize)
	handler := f.handlerOn(pool, nil, nil)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutinesBefore := runtime.NumGoroutine()
	body := `{"model":"gpt-4o","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"soak"}]}`
	var mu sync.Mutex
	statuses := map[int]int{}
	var latencies []time.Duration
	var refusals []string
	tracer.roundTrips.Store(0)
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				started := time.Now()
				recorder := f.call(ctx, handler, body)
				elapsed := time.Since(started)
				mu.Lock()
				statuses[recorder.Code]++
				if recorder.Code != http.StatusOK && len(refusals) < 5 {
					refusals = append(refusals, recorder.Body.String())
				}
				if recorder.Code == http.StatusOK {
					latencies = append(latencies, elapsed)
				}
				mu.Unlock()
				if recorder.Code == http.StatusOK && !strings.Contains(recorder.Body.String(), "[DONE]") {
					t.Errorf("a stream was cut short: %q", recorder.Body.String())
				}
			}
		}()
	}
	wg.Wait()
	roundTrips := tracer.roundTrips.Load()

	ok := statuses[http.StatusOK]
	usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var settled int64
	for _, item := range usage.Items {
		settled += item.Requests
	}
	var leases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_admission_leases WHERE integration_id = $1`, f.offer.ID).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	slices.Sort(latencies)
	percentile := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[min(len(latencies)-1, int(p*float64(len(latencies))))]
	}
	provider := time.Duration(soakChunks) * soakChunkInterval
	t.Logf("soak: %s, %d workers, pool %d, circuit breaker %v: %d calls (%.0f/s), statuses %v", duration, workers, poolSize,
		policy.CircuitBreakerFailureThreshold > 0, ok, float64(ok)/duration.Seconds(), statuses)
	t.Logf("latency p50 %s p95 %s p99 %s; provider stream %s, so gateway overhead p50 %s p99 %s",
		percentile(0.50), percentile(0.95), percentile(0.99), provider, percentile(0.50)-provider, percentile(0.99)-provider)
	t.Logf("settled %d requests, %d tokens; live leases %d; goroutines %d -> %d; heap in use %d -> %d KiB",
		settled, usage.DailyTokenCount, leases, goroutinesBefore, runtime.NumGoroutine(), before.HeapInuse>>10, after.HeapInuse>>10)
	if ok > 0 {
		t.Logf("postgres round trips: %.1f per call", float64(roundTrips)/float64(ok))
	}
	if len(statuses) != 1 || ok == 0 {
		t.Errorf("non-200 responses: %v; first: %q", statuses, refusals)
	}
	if settled != int64(ok) || usage.DailyTokenCount != int64(ok)*(soakInputTokens+soakOutputTokens) {
		t.Errorf("settlement drifted: %d calls, %d settled, %d tokens", ok, settled, usage.DailyTokenCount)
	}
	if leases != 0 {
		t.Errorf("%d admission leases leaked", leases)
	}
	if grown := runtime.NumGoroutine() - goroutinesBefore; grown > 20 {
		t.Errorf("%d goroutines leaked", grown)
	}
}
