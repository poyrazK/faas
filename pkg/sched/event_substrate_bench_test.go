// spec: §14 — Appendix D V12 measures the event substrate before its caps move.
package sched_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	eventBenchSource = "bench.orders"
	eventBenchType   = "order.created"
)

// TestEventSubstrateBench drives the production publish, fanout and drain
// code against Postgres without VMs and reports throughput and latency
// (docs/ops/event-substrate-benchmark.md). It is opt-in: it takes minutes
// and its numbers only mean something on a quiet, known host.
func TestEventSubstrateBench(t *testing.T) {
	if os.Getenv("FAAS_RUN_EVENT_SUBSTRATE_BENCH") != "1" {
		t.Skip("set FAAS_RUN_EVENT_SUBSTRATE_BENCH=1 (make bench-event-substrate) to measure the event substrate")
	}
	cfg := eventBenchConfigFromEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Publishers use apid's pool budget and the scheduler side uses schedd's,
	// as the two daemons do in production.
	schedPool := eventBenchPool(t, pool, db.DaemonMaxConnections["schedd"])
	apidPool := eventBenchPool(t, pool, db.DaemonMaxConnections["apid"])
	schedStore := state.NewPgStore(schedPool)
	publisher := &appevents.Service{Host: eventBenchHost{store: state.NewPgStore(apidPool), pool: apidPool,
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}}

	// Seed every tenant before building the engine that serves the run, so
	// its admission ledger loads their running instances as schedd's would.
	nodeID := *sched.XNewEngine(t, schedStore, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").DefaultLocalNodeID()
	tenants := seedEventBenchTenants(ctx, t, schedStore, nodeID, "stage", cfg.Accounts)
	pipelineTenants := make(map[int][]eventBenchTenant, len(cfg.PipelineRates))
	for _, rate := range cfg.PipelineRates {
		pipelineTenants[rate] = seedEventBenchTenants(ctx, t, schedStore, nodeID, fmt.Sprintf("rate%d", rate), cfg.Accounts)
	}
	eventBenchAnalyze(ctx, t, pool)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, schedStore, vmm, &sched.FakeNotifier{}, "1.10.0")
	fanoutLog := &eventBenchLogCounter{}
	loop := sched.NewLoop(schedPool, engine, slog.New(fanoutLog))

	report := eventBenchReport{Version: 1, Benchmark: "event-substrate", StartedAt: time.Now().UTC(),
		Host: eventBenchHostInfo(), Postgres: eventBenchPostgresVersion(ctx, t, pool), Config: cfg}

	// Isolated stages: each one runs alone against the backlog the previous
	// stage left, so its rate is that stage's ceiling on this host.
	published, publish := runEventBenchPublish(ctx, t, publisher, tenants, cfg.Events, cfg.Publishers)
	report.Stages = append(report.Stages, publish)
	// Production tables carry planner statistics; a fresh database has none
	// for tables that grew during publishing.
	eventBenchAnalyze(ctx, t, pool)
	routed, fanout := runEventBenchFanout(ctx, t, loop, schedPool, published)
	report.Stages = append(report.Stages, fanoutLog.annotate(fanout))
	report.Stages = append(report.Stages, runEventBenchDrain(ctx, t, schedStore, engine, schedPool, cfg, routed))

	// End-to-end: offered load with fanout scheduled exactly as schedd does
	// and the drain running beside it.
	for _, rate := range cfg.PipelineRates {
		stage := runEventBenchPipeline(ctx, t, loop, publisher, schedStore, engine, schedPool, cfg, pipelineTenants[rate], rate)
		report.Stages = append(report.Stages, fanoutLog.annotate(stage))
	}
	if boots := *vmm.ColdBoots(); boots != 0 {
		t.Fatalf("drain cold-booted %d fake instances; the benchmark must stay on the warm wake path", boots)
	}
	for _, stage := range report.Stages {
		t.Log(stage.line())
	}
	if dir := os.Getenv("FAAS_EVENT_SUBSTRATE_BENCH_DIR"); dir != "" {
		writeEventBenchReport(t, filepath.Join(dir, "event-pipeline.json"), report)
	}
}

type eventBenchConfig struct {
	Events             int   `json:"events"`
	Accounts           int   `json:"accounts"`
	Publishers         int   `json:"publishers"`
	DrainConcurrency   int   `json:"drain_concurrency"`
	HandlerMS          int   `json:"handler_ms"`
	PipelineRates      []int `json:"pipeline_rates_per_second"`
	PipelineSeconds    int   `json:"pipeline_seconds"`
	PipelineSettleSecs int   `json:"pipeline_settle_seconds"`
}

func eventBenchConfigFromEnv(t *testing.T) eventBenchConfig {
	t.Helper()
	cfg := eventBenchConfig{
		Events:             eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_EVENTS", api.EventSubstrateBenchEvents),
		Accounts:           eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_ACCOUNTS", api.EventSubstrateBenchAccounts),
		Publishers:         eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_PUBLISHERS", int(db.DaemonMaxConnections["apid"])),
		DrainConcurrency:   eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_DRAIN_CONCURRENCY", 0),
		HandlerMS:          eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_HANDLER_MS", 0),
		PipelineRates:      slices.Clone(api.EventSubstrateBenchPipelineRates),
		PipelineSeconds:    eventBenchEnvInt(t, "FAAS_EVENT_SUBSTRATE_BENCH_PIPELINE_SECONDS", api.EventSubstrateBenchPipelineSeconds),
		PipelineSettleSecs: int(api.EventSubstrateBenchPipelineSettle / time.Second),
	}
	if raw := os.Getenv("FAAS_EVENT_SUBSTRATE_BENCH_PIPELINE_RATES"); raw != "" {
		cfg.PipelineRates = nil
		for _, field := range strings.Split(raw, ",") {
			rate, err := strconv.Atoi(strings.TrimSpace(field))
			if err != nil || rate <= 0 {
				t.Fatalf("FAAS_EVENT_SUBSTRATE_BENCH_PIPELINE_RATES: %q is not a positive rate", field)
			}
			cfg.PipelineRates = append(cfg.PipelineRates, rate)
		}
	}
	if cfg.Events <= 0 || cfg.Accounts <= 0 || cfg.Publishers <= 0 || cfg.PipelineSeconds <= 0 {
		t.Fatalf("event substrate bench config must be positive: %+v", cfg)
	}
	return cfg
}

func eventBenchEnvInt(t *testing.T, key string, fallback int) int {
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

func eventBenchPool(t *testing.T, base *pgxpool.Pool, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg := base.Config().Copy()
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open bench pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// eventBenchHost is the part of apid the publish handler reaches through
// appevents.Host; Notify sends the same pg_notify wake apid sends.
type eventBenchHost struct {
	store state.Store
	pool  *pgxpool.Pool
	log   *slog.Logger
}

func (h eventBenchHost) LoadApp(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool) {
	return state.App{}, false
}
func (h eventBenchHost) Store() state.Store                                         { return h.store }
func (h eventBenchHost) WorkflowRuntimeEnabled() bool                               { return false }
func (h eventBenchHost) Logger() *slog.Logger                                       { return h.log }
func (h eventBenchHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h eventBenchHost) Notify(ctx context.Context, channel, payload string) error {
	_, err := h.pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	return err
}

// eventBenchTenant is one Scale account with one live app, a running
// instance (so the drain takes the warm wake path) and one subscription.
type eventBenchTenant struct {
	account        state.Account
	subscriptionID string
}

func seedEventBenchTenants(ctx context.Context, t *testing.T, store *state.PgStore, nodeID, prefix string, count int) []eventBenchTenant {
	t.Helper()
	tenants := make([]eventBenchTenant, 0, count)
	for i := range count {
		name := fmt.Sprintf("bench-%s-%d", prefix, i)
		account, err := store.CreateAccount(ctx, name+"@example.test", api.PlanScale)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: name, Type: state.AppTypeApp,
			RAMMB: 512, MaxConcurrency: 20, IdleTimeoutS: 600})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:event-substrate-bench", Status: state.DeployPending})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatalf("MarkDeploymentLive: %v", err)
		}
		if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 512, nodeID, ""); err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		subscription, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, eventBenchSource, eventBenchType, nil)
		if err != nil {
			t.Fatalf("UpsertEventSubscription: %v", err)
		}
		tenants = append(tenants, eventBenchTenant{account: account, subscriptionID: subscription.ID})
	}
	return tenants
}

// eventBenchEvent ties a published event to the invocation its delivery
// becomes, so latency can be measured from publish to handler.
type eventBenchEvent struct {
	invocationID string
	publishedAt  time.Time
}

func publishEventBenchEvent(ctx context.Context, publisher *appevents.Service, tenant eventBenchTenant, id string) (eventBenchEvent, time.Duration, error) {
	body, err := json.Marshal(api.PublishEventRequest{ID: id, Source: eventBenchSource, Type: eventBenchType, Data: json.RawMessage(`{"bench":true}`)})
	if err != nil {
		return eventBenchEvent{}, 0, err
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/events:publish", bytes.NewReader(body)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	started := time.Now()
	publisher.PublishEvent(recorder, request, tenant.account)
	elapsed := time.Since(started)
	if recorder.Code != http.StatusAccepted {
		return eventBenchEvent{}, elapsed, fmt.Errorf("publish %s: status %d: %s", id, recorder.Code, recorder.Body.String())
	}
	return eventBenchEvent{
		invocationID: state.PublishedEventInvocationID(tenant.account.ID, eventBenchSource, id, tenant.subscriptionID),
		publishedAt:  started,
	}, elapsed, nil
}

func runEventBenchPublish(ctx context.Context, t *testing.T, publisher *appevents.Service, tenants []eventBenchTenant, total, workers int) ([]eventBenchEvent, eventBenchStage) {
	t.Helper()
	events := make([]eventBenchEvent, total)
	latencies := make([]time.Duration, total)
	var errors atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	started := time.Now()
	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := worker; i < total; i += workers {
				event, latency, err := publishEventBenchEvent(ctx, publisher, tenants[i%len(tenants)], fmt.Sprintf("stage-%d", i))
				if err != nil {
					errors.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				events[i], latencies[i] = event, latency
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)
	if n := errors.Load(); n > 0 {
		t.Fatalf("publish stage: %d of %d publishes failed; first: %v", n, total, firstErr.Load())
	}
	return events, eventBenchStage{Name: "publish", Items: total, Seconds: elapsed.Seconds(),
		PerSecond: float64(total) / elapsed.Seconds(), LatencyMS: eventBenchPercentiles(latencies),
		Note: fmt.Sprintf("%d concurrent publishers through appevents.Service.PublishEvent (validation, storage admission, outbox trigger, receipt); excludes HTTP auth middleware", workers)}
}

// runEventBenchFanout routes the backlog with back-to-back sweeps. A stage
// that cannot route it all before the stage timeout is a measurement, not a
// failure: it reports what it routed and what is still pending.
func runEventBenchFanout(ctx context.Context, t *testing.T, loop *sched.Loop, pool *pgxpool.Pool, events []eventBenchEvent) (int, eventBenchStage) {
	t.Helper()
	deadline := time.Now().Add(api.EventSubstrateBenchStageTimeout)
	var sweeps []time.Duration
	routed := 0
	started := time.Now()
	for routed < len(events) && time.Now().Before(deadline) {
		sweepStarted := time.Now()
		loop.XRunEventFanoutSweep(ctx)
		sweeps = append(sweeps, time.Since(sweepStarted))
		routed = eventBenchCount(ctx, t, pool, "SELECT count(*) FROM invocations WHERE source = 'async_invoke'")
	}
	elapsed := time.Since(started)
	note := fmt.Sprintf("back-to-back sweeps over a %d-event backlog; latency is per sweep (%d sweeps)", len(events), len(sweeps))
	if routed < len(events) {
		note += fmt.Sprintf("; stage timeout %s reached", api.EventSubstrateBenchStageTimeout)
	}
	return routed, eventBenchStage{Name: "fanout", Items: routed, Pending: len(events) - routed, Seconds: elapsed.Seconds(),
		PerSecond: float64(routed) / elapsed.Seconds(), LatencyMS: eventBenchPercentiles(sweeps), Note: note}
}

// eventBenchGateway stands in for gatewayd-internal: it records when each
// invocation reached the "handler" and answers 200 after the configured
// handler time, so the drain runs its real claim/complete path.
type eventBenchGateway struct {
	handlerTime time.Duration
	mu          sync.Mutex
	handled     map[string]time.Time
}

func newEventBenchGateway(handlerMS int) *eventBenchGateway {
	return &eventBenchGateway{handlerTime: time.Duration(handlerMS) * time.Millisecond, handled: map[string]time.Time{}}
}

func (g *eventBenchGateway) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (g *eventBenchGateway) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	g.mu.Lock()
	if _, seen := g.handled[inv.ID]; !seen {
		g.handled[inv.ID] = time.Now()
	}
	g.mu.Unlock()
	if g.handlerTime > 0 {
		select {
		case <-time.After(g.handlerTime):
		case <-ctx.Done():
			return state.Invocation{}, ctx.Err()
		}
	}
	inv.ResponseStatusCode = http.StatusOK
	inv.Result = json.RawMessage(`{}`)
	return inv, nil
}

func (g *eventBenchGateway) InvokeWithWake(ctx context.Context, appID string, inv state.Invocation, _ sched.WakeResult) (state.Invocation, error) {
	return g.Invoke(ctx, appID, inv)
}

func (g *eventBenchGateway) handledAt(id string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.handled[id]
	return at, ok
}

func startEventBenchDrain(ctx context.Context, t *testing.T, store *state.PgStore, engine *sched.Engine, pool *pgxpool.Pool, cfg eventBenchConfig, gateway *eventBenchGateway, logs *eventBenchLogCounter) func() {
	t.Helper()
	opts := []sched.DrainOption{sched.WithDrainGatewaySynth(gateway), sched.WithDrainLogger(slog.New(logs))}
	if cfg.DrainConcurrency > 0 {
		opts = append(opts, sched.WithDrainDispatchConcurrency(cfg.DrainConcurrency))
	}
	drain := sched.NewDrain(store, engine, opts...)
	drainCtx, stop := context.WithCancel(ctx)
	notifications, err := db.SubscribeWithReconnect(drainCtx, pool, []string{db.NotifyInvocationDue}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		stop()
		t.Fatalf("subscribe invocation_due: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = drain.Run(drainCtx, notifications)
	}()
	return func() {
		stop()
		<-done
	}
}

// runEventBenchDrain drains the invocations the fanout stage routed and,
// like fanout, reports partial progress at the stage timeout.
func runEventBenchDrain(ctx context.Context, t *testing.T, store *state.PgStore, engine *sched.Engine, pool *pgxpool.Pool, cfg eventBenchConfig, routed int) eventBenchStage {
	t.Helper()
	gateway := newEventBenchGateway(cfg.HandlerMS)
	logs := &eventBenchLogCounter{}
	started := time.Now()
	stop := startEventBenchDrain(ctx, t, store, engine, pool, cfg, gateway, logs)
	defer stop()
	deadline := started.Add(api.EventSubstrateBenchStageTimeout)
	completed := 0
	for completed < routed && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		completed = eventBenchCount(ctx, t, pool, "SELECT count(*) FROM invocations WHERE source = 'async_invoke' AND state = 'completed'")
	}
	elapsed := time.Since(started)
	concurrency := cfg.DrainConcurrency
	label := strconv.Itoa(concurrency)
	if concurrency == 0 {
		label = "production default"
	}
	stage := eventBenchStage{Name: "drain", Items: completed, Pending: routed - completed, Seconds: elapsed.Seconds(),
		PerSecond: float64(completed) / elapsed.Seconds(), Attempts: eventBenchAttempts(ctx, t, pool),
		Note: fmt.Sprintf("claim, warm wake, gateway dispatch and completion over the routed backlog; dispatch concurrency %s; handler time %d ms", label, cfg.HandlerMS)}
	return logs.annotate(stage)
}

// eventBenchAttempts counts completed event deliveries by dispatch attempts,
// with the most common last errors, so retries cannot hide in a rate.
func eventBenchAttempts(ctx context.Context, t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT attempts::text, count(*) FROM invocations WHERE source = 'async_invoke' GROUP BY attempts
		UNION ALL SELECT 'last_error: ' || left(last_error, 160), count(*) FROM invocations
		WHERE source = 'async_invoke' AND coalesce(last_error, '') <> '' GROUP BY left(last_error, 160)`)
	if err != nil {
		t.Fatalf("attempts: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			t.Fatalf("attempts scan: %v", err)
		}
		counts[key] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("attempts rows: %v", err)
	}
	return counts
}

func eventBenchAnalyze(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
}

// eventBenchLogCounter keeps warnings from the scheduler code under test:
// a failed claim or a retried dispatch only shows up in its logs, and a
// stage rate that silently includes retries is not a capacity number.
type eventBenchLogCounter struct {
	mu       sync.Mutex
	warnings int
	samples  []string
}

func (c *eventBenchLogCounter) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (c *eventBenchLogCounter) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings++
	if len(c.samples) < 3 {
		message := record.Message
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "err" || attr.Key == "error" {
				message += ": " + attr.Value.String()
			}
			return true
		})
		c.samples = append(c.samples, message)
	}
	return nil
}

func (c *eventBenchLogCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *eventBenchLogCounter) WithGroup(string) slog.Handler      { return c }

// annotate moves the warnings logged since the last call onto stage.
func (c *eventBenchLogCounter) annotate(stage eventBenchStage) eventBenchStage {
	c.mu.Lock()
	defer c.mu.Unlock()
	stage.Warnings += c.warnings
	stage.WarningSamples = append(stage.WarningSamples, c.samples...)
	c.warnings, c.samples = 0, nil
	return stage
}

// runEventBenchFanoutLikeSchedd schedules sweeps as Loop.Run does: every
// event_published wake submits a sweep into a single slot, a wake that
// arrives while one runs is dropped, and a safety tick catches the rest.
func runEventBenchFanoutLikeSchedd(ctx context.Context, t *testing.T, loop *sched.Loop, pool *pgxpool.Pool) func() {
	t.Helper()
	fanoutCtx, stop := context.WithCancel(ctx)
	notifications, err := db.SubscribeWithReconnect(fanoutCtx, pool, []string{db.NotifyEventPublished}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		stop()
		t.Fatalf("subscribe event_published: %v", err)
	}
	var running atomic.Bool
	var sweeps sync.WaitGroup
	sweep := func() {
		if !running.CompareAndSwap(false, true) {
			return
		}
		sweeps.Add(1)
		go func() {
			defer sweeps.Done()
			defer running.Store(false)
			loop.XRunEventFanoutSweep(fanoutCtx)
		}()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(sched.XEventFanoutSafetyTick)
		defer ticker.Stop()
		sweep()
		for {
			select {
			case <-fanoutCtx.Done():
				return
			case notification, ok := <-notifications:
				if !ok {
					return
				}
				if notification.Channel == db.NotifyEventPublished {
					sweep()
				}
			case <-ticker.C:
				sweep()
			}
		}
	}()
	return func() {
		stop()
		<-done
		sweeps.Wait()
	}
}

func runEventBenchPipeline(ctx context.Context, t *testing.T, loop *sched.Loop, publisher *appevents.Service, store *state.PgStore, engine *sched.Engine, pool *pgxpool.Pool, cfg eventBenchConfig, tenants []eventBenchTenant, rate int) eventBenchStage {
	t.Helper()
	gateway := newEventBenchGateway(cfg.HandlerMS)
	drainLogs := &eventBenchLogCounter{}
	stopDrain := startEventBenchDrain(ctx, t, store, engine, pool, cfg, gateway, drainLogs)
	defer stopDrain()
	stopFanout := runEventBenchFanoutLikeSchedd(ctx, t, loop, pool)
	defer stopFanout()

	total := rate * cfg.PipelineSeconds
	events := make([]eventBenchEvent, total)
	queue := make(chan int, total)
	var errors atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for range cfg.Publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				event, _, err := publishEventBenchEvent(ctx, publisher, tenants[i%len(tenants)], fmt.Sprintf("rate%d-%d", rate, i))
				if err != nil {
					errors.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				events[i] = event
			}
		}()
	}
	interval := time.Second / time.Duration(rate)
	started := time.Now()
	for i := range total {
		if wait := time.Until(started.Add(time.Duration(i) * interval)); wait > 0 {
			time.Sleep(wait)
		}
		queue <- i
	}
	close(queue)
	wg.Wait()
	publishedFor := time.Since(started)
	if n := errors.Load(); n > 0 {
		t.Fatalf("pipeline at %d/s: %d of %d publishes failed; first: %v", rate, n, total, firstErr.Load())
	}

	settleBy := time.Now().Add(api.EventSubstrateBenchPipelineSettle)
	var latencies []time.Duration
	var lastHandled time.Time
	pending := total
	for {
		latencies, lastHandled, pending = eventBenchHandled(gateway, events)
		if pending == 0 || time.Now().After(settleBy) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stage := eventBenchStage{Name: fmt.Sprintf("pipeline@%d/s", rate), Items: total - pending, Pending: pending,
		OfferedPerSecond: float64(rate), LatencyMS: eventBenchPercentiles(latencies),
		Note: fmt.Sprintf("%d s of offered load, publish to handler; fanout on event_published wakes plus a %s safety tick; publishing took %.1f s", cfg.PipelineSeconds, sched.XEventFanoutSafetyTick, publishedFor.Seconds())}
	if !lastHandled.IsZero() {
		stage.Seconds = lastHandled.Sub(started).Seconds()
		stage.PerSecond = float64(stage.Items) / stage.Seconds
	}
	return drainLogs.annotate(stage)
}

func eventBenchHandled(gateway *eventBenchGateway, events []eventBenchEvent) ([]time.Duration, time.Time, int) {
	latencies := make([]time.Duration, 0, len(events))
	var last time.Time
	pending := 0
	for _, event := range events {
		handledAt, ok := gateway.handledAt(event.invocationID)
		if !ok {
			pending++
			continue
		}
		latencies = append(latencies, handledAt.Sub(event.publishedAt))
		if handledAt.After(last) {
			last = handledAt
		}
	}
	return latencies, last, pending
}

func eventBenchCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

type eventBenchLatency struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

func eventBenchPercentiles(samples []time.Duration) *eventBenchLatency {
	if len(samples) == 0 {
		return nil
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	at := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(sorted)))) - 1
		return float64(sorted[max(index, 0)]) / float64(time.Millisecond)
	}
	return &eventBenchLatency{P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: at(1)}
}

type eventBenchStage struct {
	Name             string             `json:"name"`
	Items            int                `json:"items"`
	Pending          int                `json:"pending_at_end,omitempty"`
	Seconds          float64            `json:"seconds"`
	PerSecond        float64            `json:"per_second"`
	OfferedPerSecond float64            `json:"offered_per_second,omitempty"`
	LatencyMS        *eventBenchLatency `json:"latency_ms,omitempty"`
	Attempts         map[string]int     `json:"attempts,omitempty"`
	Warnings         int                `json:"warnings"`
	WarningSamples   []string           `json:"warning_samples,omitempty"`
	Note             string             `json:"note"`
}

func (s eventBenchStage) line() string {
	line := fmt.Sprintf("event-substrate: stage=%s items=%d per_second=%.1f seconds=%.2f", s.Name, s.Items, s.PerSecond, s.Seconds)
	if s.OfferedPerSecond > 0 {
		line += fmt.Sprintf(" offered_per_second=%.0f pending=%d", s.OfferedPerSecond, s.Pending)
	}
	if s.LatencyMS != nil {
		line += fmt.Sprintf(" p50_ms=%.1f p95_ms=%.1f p99_ms=%.1f max_ms=%.1f", s.LatencyMS.P50, s.LatencyMS.P95, s.LatencyMS.P99, s.LatencyMS.Max)
	}
	line += fmt.Sprintf(" warnings=%d", s.Warnings)
	if len(s.Attempts) > 0 {
		keys := slices.Sorted(maps.Keys(s.Attempts))
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", key, s.Attempts[key]))
		}
		line += " attempts{" + strings.Join(parts, " ") + "}"
	}
	for _, sample := range s.WarningSamples {
		line += "\n    warning: " + sample
	}
	return line
}

type eventBenchHostDetails struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	Go       string `json:"go"`
}

type eventBenchReport struct {
	Version   int                   `json:"version"`
	Benchmark string                `json:"benchmark"`
	StartedAt time.Time             `json:"started_at"`
	Host      eventBenchHostDetails `json:"host"`
	Postgres  string                `json:"postgres_version"`
	Config    any                   `json:"config"`
	Stages    []eventBenchStage     `json:"stages"`
}

func eventBenchHostInfo() eventBenchHostDetails {
	hostname, _ := os.Hostname()
	return eventBenchHostDetails{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Go: runtime.Version()}
}

func eventBenchPostgresVersion(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var version string
	if err := pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatalf("server_version: %v", err)
	}
	return version
}

func writeEventBenchReport(t *testing.T, path string, report any) {
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
