package webhook

// adr: 076 — spec Appendix D V12 measures outbound delivery with production defaults.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestWebhookDeliveryBench measures the outbound webhook dispatcher against
// Postgres and a local receiver, with the production tick, claim cap and
// in-flight limit unless overridden (docs/ops/event-substrate-benchmark.md).
func TestWebhookDeliveryBench(t *testing.T) {
	if os.Getenv("FAAS_RUN_EVENT_SUBSTRATE_BENCH") != "1" {
		t.Skip("set FAAS_RUN_EVENT_SUBSTRATE_BENCH=1 (make bench-event-substrate) to measure webhook delivery")
	}
	deliveries := webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_DELIVERIES", api.EventSubstrateBenchWebhookDeliveries)
	subscribers := webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_SUBSCRIBERS", api.EventSubstrateBenchWebhookSubscribers)
	receiverMS := webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_RECEIVER_MS", 0)
	tick := time.Duration(webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_TICK_MS", int(DefaultTick/time.Millisecond))) * time.Millisecond
	claimCap := webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_CAP", DefaultCap)
	maxInFlight := webhookBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_MAX_IN_FLIGHT", DefaultMaxInFlight)
	if deliveries <= 0 || subscribers <= 0 || tick <= 0 || claimCap <= 0 || maxInFlight <= 0 {
		t.Fatal("webhook delivery bench config must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	base := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, base); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	poolConfig := base.Config().Copy()
	poolConfig.MaxConns = db.DaemonMaxConnections["schedd"]
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open schedd-sized pool: %v", err)
	}
	t.Cleanup(pool.Close)
	store := state.NewPgStore(pool)

	var mu sync.Mutex
	received := map[string]time.Time{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		if id := r.Header.Get("X-Faas-Delivery-Id"); id != "" {
			if _, seen := received[id]; !seen {
				received[id] = time.Now()
			}
		}
		mu.Unlock()
		if receiverMS > 0 {
			time.Sleep(time.Duration(receiverMS) * time.Millisecond)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	account, err := store.CreateAccount(ctx, "webhook-bench@example.test", api.PlanScale)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "webhook-bench", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 20, IdleTimeoutS: 600})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	loader, sealed := identityForSealedBlob(t)
	hooks := make([]state.AppWebhook, 0, subscribers)
	for i := range subscribers {
		hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: account.ID, AppID: app.ID,
			TargetURL: fmt.Sprintf("%s/hooks/%d", target.URL, i), SecretSealed: sealed, Enabled: true})
		if err != nil {
			t.Fatalf("CreateAppWebhook: %v", err)
		}
		hooks = append(hooks, hook)
	}
	enqueuedAt := make(map[string]time.Time, deliveries)
	for i := range deliveries {
		hook := hooks[i%len(hooks)]
		delivery, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: app.ID, AccountID: account.ID,
			Event: state.AppWebhookEventDeploymentLive, Payload: json.RawMessage(fmt.Sprintf(`{"bench":%d}`, i))})
		if err != nil {
			t.Fatalf("RecordAppWebhookDelivery: %v", err)
		}
		enqueuedAt[delivery.ID] = time.Now()
	}

	dispatcher := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithTick(tick).WithCap(claimCap).WithMaxInFlight(maxInFlight)
	dispatcher.IdentityLoader, dispatcher.HTTPClient = loader, target.Client()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(done)
		_ = dispatcher.Run(runCtx)
	}()
	deadline := started.Add(api.EventSubstrateBenchStageTimeout)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= deliveries {
			break
		}
		if time.Now().After(deadline) {
			stop()
			<-done
			t.Fatalf("webhook stage: %d of %d deliveries received before the stage timeout", n, deliveries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(started)
	stop()
	<-done

	latencies := make([]time.Duration, 0, deliveries)
	mu.Lock()
	for id, at := range received {
		if queued, ok := enqueuedAt[id]; ok {
			latencies = append(latencies, at.Sub(queued))
		}
	}
	mu.Unlock()
	stage := webhookBenchStage{Name: "webhook", Items: deliveries, Seconds: elapsed.Seconds(), PerSecond: float64(deliveries) / elapsed.Seconds(),
		LatencyMS: webhookBenchPercentiles(latencies),
		Note: fmt.Sprintf("%d subscribers; tick %s, claim cap %d, max in flight %d, receiver %d ms; latency is enqueue to receiver, backlog enqueued before the dispatcher started",
			subscribers, tick, claimCap, maxInFlight, receiverMS)}
	t.Logf("event-substrate: stage=%s items=%d per_second=%.1f seconds=%.2f p50_ms=%.1f p95_ms=%.1f p99_ms=%.1f max_ms=%.1f",
		stage.Name, stage.Items, stage.PerSecond, stage.Seconds, stage.LatencyMS.P50, stage.LatencyMS.P95, stage.LatencyMS.P99, stage.LatencyMS.Max)
	if dir := os.Getenv("FAAS_EVENT_SUBSTRATE_BENCH_DIR"); dir != "" {
		hostname, _ := os.Hostname()
		writeWebhookBenchReport(t, filepath.Join(dir, "webhook-delivery.json"), map[string]any{
			"version": 1, "benchmark": "webhook-delivery", "started_at": started.UTC(),
			"host":   map[string]any{"hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "go": runtime.Version()},
			"stages": []webhookBenchStage{stage},
		})
	}
}

func webhookBenchEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		t.Fatalf("%s=%q: want a non-negative integer", key, raw)
	}
	return value
}

type webhookBenchLatency struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type webhookBenchStage struct {
	Name      string               `json:"name"`
	Items     int                  `json:"items"`
	Seconds   float64              `json:"seconds"`
	PerSecond float64              `json:"per_second"`
	LatencyMS *webhookBenchLatency `json:"latency_ms"`
	Note      string               `json:"note"`
}

func webhookBenchPercentiles(samples []time.Duration) *webhookBenchLatency {
	if len(samples) == 0 {
		return &webhookBenchLatency{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	at := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(sorted)))) - 1
		return float64(sorted[max(index, 0)]) / float64(time.Millisecond)
	}
	return &webhookBenchLatency{P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: at(1)}
}

func writeWebhookBenchReport(t *testing.T, path string, report any) {
	t.Helper()
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("report dir: %v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("event-substrate: report=%s", path)
}
