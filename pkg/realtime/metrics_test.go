package realtime

// adr: 299

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// TestStatsCollectorExposesFixedCardinalityMetrics pins ADR-015's realtime
// observability contract: manager counters are exported without endpoint,
// app, principal, or connection labels.
func TestStatsCollectorExposesFixedCardinalityMetrics(t *testing.T) {
	manager := NewManager(Config{}, NopHooks{})
	defer func() { _ = manager.Close() }()
	manager.acceptedConnections.Store(3)
	manager.receivedBytes.Store(128)
	manager.callbackErrors.Store(1)
	manager.callbackOutboxFull.Store(2)
	manager.callbackOutboxAdmissionErrors.Store(3)
	manager.callbackUnpersistedFailures.Store(4)
	manager.recordAuthOutcome(authMetricModeStaticBearer, authMetricOutcomeAccepted)
	manager.recordAuthOutcome(authMetricModeStaticBearer, authMetricOutcomeRejected)
	manager.recordAuthOutcome(authMetricModeStaticBearer, authMetricOutcomeRejected)

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStatsCollector(manager))
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, metric := range []string{
		`realtimed_accepted_connections_total 3`,
		`realtimed_received_bytes_total 128`,
		`realtimed_callback_errors_total 1`,
		`realtimed_callback_outbox_full_total 2`,
		`realtimed_callback_outbox_admission_errors_total 3`,
		`realtimed_callback_unpersisted_failures_total 4`,
		`realtimed_callback_pending_capacity_bytes 0`,
		`realtimed_auth_outcomes_total{mode="static_bearer",outcome="accepted"} 1`,
		`realtimed_auth_outcomes_total{mode="static_bearer",outcome="rejected"} 2`,
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("metric %q missing from:\n%s", metric, text)
		}
	}
	if strings.Contains(text, "endpoint_id=") || strings.Contains(text, "principal=") || strings.Contains(text, "token=") {
		t.Fatalf("auth metrics exposed an unbounded label:\n%s", text)
	}
}

func TestStatsCollectorExposesCallbackOutboxRetention(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxAttempts: 1})
	event := testCallbackEvent()
	if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim = (%v, %v)", claimed, err)
	}
	queue.deadMaxBytes = 1
	if err := queue.Fail(event.ID); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer func() { _ = manager.Close() }()
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStatsCollector(manager))
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := recorder.Body.String()
	for _, metric := range []string{
		"realtimed_callback_pending 0",
		"realtimed_callback_pending_bytes 0",
		"realtimed_callback_dead_letters 0",
		"realtimed_callback_dead_letter_bytes 0",
		"realtimed_callback_dead_letter_capacity_bytes 1",
		"realtimed_callback_dead_letter_evictions_total 1",
		"realtimed_callback_dead_letter_last_eviction_timestamp_seconds ",
	} {
		if !strings.Contains(text, metric) {
			t.Errorf("metric %q missing from:\n%s", metric, text)
		}
	}
}

func TestStatsCollectorExposesCallbackOutboxCapacity(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{MaxBytes: 4096})
	manager := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer func() { _ = manager.Close() }()

	if got := manager.Stats().CallbackPendingCapacityBytes; got != 4096 {
		t.Fatalf("pending capacity bytes = %d, want 4096", got)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStatsCollector(manager))
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), "realtimed_callback_pending_capacity_bytes 4096") {
		t.Fatalf("pending capacity gauge missing from:\n%s", recorder.Body.String())
	}
}

func TestStatsCollectorExposesCallbackReplayProgress(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{})
	replayed := testCallbackEvent()
	replayed.ID = "evt_metrics_replayed"
	replayed.ConnectionID = "connection-replayed"
	if claimed, err := queue.EnqueueAndClaim(replayed); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim replayed = (%v, %v)", claimed, err)
	}
	queue.Release(replayed.ID)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.Run(ctx, func(context.Context, Event) error {
			cancel()
			return nil
		})
	}()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}

	pending := testCallbackEvent()
	pending.ID = "evt_metrics_pending"
	pending.ConnectionID = "connection-pending"
	if claimed, err := queue.EnqueueAndClaim(pending); err != nil || !claimed {
		t.Fatalf("EnqueueAndClaim pending = (%v, %v)", claimed, err)
	}
	queue.Release(pending.ID)

	manager := NewManager(Config{}, HTTPHooks{DurableQueue: queue})
	defer func() { _ = manager.Close() }()
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewStatsCollector(manager))
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := recorder.Body.String()
	if !strings.Contains(text, "realtimed_callback_replay_deliveries_total 1") {
		t.Fatalf("replay delivery counter missing from:\n%s", text)
	}
	if !strings.Contains(text, "realtimed_callback_replay_attempts_total 1") {
		t.Fatalf("replay attempt counter missing from:\n%s", text)
	}
	if !strings.Contains(text, "realtimed_callback_replay_ready 1") {
		t.Fatalf("replay ready gauge missing from:\n%s", text)
	}
	if !strings.Contains(text, "realtimed_callback_replay_delayed 0") {
		t.Fatalf("replay delayed gauge missing from:\n%s", text)
	}
	if !strings.Contains(text, "realtimed_callback_oldest_pending_age_seconds ") {
		t.Fatalf("oldest pending age gauge missing from:\n%s", text)
	}
}
