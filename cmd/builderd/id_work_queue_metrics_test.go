package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func notificationSamples(reg *prometheus.Registry) (map[string]float64, error) {
	families, err := reg.Gather()
	if err != nil {
		return nil, err
	}
	samples := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.Metric {
			kind, outcome := "", ""
			for _, label := range metric.Label {
				switch label.GetName() {
				case "queue":
					kind = label.GetValue()
				case "outcome":
					outcome = label.GetValue()
				default:
					return nil, fmt.Errorf("unexpected label %q", label.GetName())
				}
			}
			if kind != "build" && kind != "cancel" {
				return nil, fmt.Errorf("unexpected queue %q", kind)
			}
			value := metric.GetGauge().GetValue()
			if metric.Counter != nil {
				value = metric.GetCounter().GetValue()
			}
			samples[family.GetName()+"/"+kind+"/"+outcome] = value
		}
	}
	if len(families) != 7 || len(samples) != 22 {
		return nil, fmt.Errorf("incomplete queue observations: %d families, %d samples", len(families), len(samples))
	}
	return samples, nil
}

func requireNotificationSamples(t *testing.T, reg *prometheus.Registry, kind string, want map[string]float64) {
	t.Helper()
	samples, err := notificationSamples(reg)
	if err != nil {
		t.Fatal(err)
	}
	for suffix, expected := range want {
		metric, outcome, _ := strings.Cut(suffix, "/")
		key := "builderd_notification_queue_" + metric + "/" + kind + "/" + outcome
		if got, ok := samples[key]; !ok || got != expected {
			t.Errorf("%s = %v (present=%v), want %v", key, got, ok, expected)
		}
	}
}

func TestIDWorkQueueMetricsOutcomesAndWaiting(t *testing.T) {
	for _, kind := range []string{"build", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			started := make(chan string, 3)
			release := make(chan struct{})
			q := newIDWorkQueue(ctx, 1, 1, func(ctx context.Context, id string) {
				started <- id
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
			other := newIDWorkQueue(ctx, 3, 2, func(context.Context, string) {})
			t.Cleanup(func() { cancel(); q.Wait(); other.Wait() })
			reg := prometheus.NewPedanticRegistry()
			if kind == "build" {
				registerIDWorkQueueMetrics(reg, q, other)
			} else {
				registerIDWorkQueueMetrics(reg, other, q)
			}
			requireNotificationSamples(t, reg, kind, map[string]float64{
				"waiting/": 0, "active/": 0, "capacity/": 1, "workers/": 1, "enabled/": 1,
				"oldest_enqueued_timestamp_seconds/": 0,
				"enqueues_total/accepted":            0, "enqueues_total/coalesced": 0, "enqueues_total/full": 0,
				"enqueues_total/stopping": 0, "enqueues_total/invalid": 0,
			})
			if !q.Enqueue("private-build-a") {
				t.Fatal("first notification rejected")
			}
			waitForQueueItem(t, started, "private-build-a")
			// A blocked handler is active, never an oldest waiting notification.
			requireNotificationSamples(t, reg, kind, map[string]float64{"waiting/": 0, "active/": 1, "oldest_enqueued_timestamp_seconds/": 0})
			if q.Enqueue("") || q.Enqueue("private-build-a") || !q.Enqueue("private-build-b") || q.Enqueue("private-build-b") || q.Enqueue("private-build-c") {
				t.Fatal("invalid, duplicate, accepted or full outcome changed")
			}
			q.mu.Lock()
			queuedAt := q.pending["private-build-b"]
			if queuedAt.IsZero() || queuedAt.After(time.Now()) {
				t.Errorf("invalid actual enqueue time: %v", queuedAt)
			}
			q.pending["private-build-b"] = time.Unix(1700000000, 0)
			q.mu.Unlock()
			requireNotificationSamples(t, reg, kind, map[string]float64{
				"waiting/": 1, "active/": 1, "oldest_enqueued_timestamp_seconds/": 1700000000,
				"enqueues_total/accepted": 2, "enqueues_total/coalesced": 2, "enqueues_total/full": 1,
				"enqueues_total/stopping": 0, "enqueues_total/invalid": 1,
			})
			// Scrapes and duplicate arrivals continue while the handler is blocked.
			done := make(chan error, 20)
			for range 20 {
				go func() {
					if q.Enqueue("private-build-a") {
						done <- fmt.Errorf("active duplicate accepted")
						return
					}
					_, err := notificationSamples(reg)
					done <- err
				}()
			}
			for range 20 {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("metrics or duplicate enqueue blocked on handler")
				}
			}
			if q.Enqueue("private-build-b") {
				t.Fatal("queued duplicate accepted")
			}
			requireNotificationSamples(t, reg, kind, map[string]float64{"enqueues_total/coalesced": 23, "oldest_enqueued_timestamp_seconds/": 1700000000})
			release <- struct{}{}
			waitForQueueItem(t, started, "private-build-b")
			requireNotificationSamples(t, reg, kind, map[string]float64{"waiting/": 0, "active/": 1, "oldest_enqueued_timestamp_seconds/": 0})
			if !q.Enqueue("private-build-a") {
				t.Fatal("completed ID remained coalesced")
			}
			cancel()
			if q.Enqueue("private-build-d") {
				t.Fatal("notification accepted after shutdown")
			}
			q.Wait()
			requireNotificationSamples(t, reg, kind, map[string]float64{"enabled/": 0, "active/": 0, "enqueues_total/accepted": 3, "enqueues_total/stopping": 1, "enqueues_total/full": 1})
		})
	}
}

func TestIDWorkQueueMetricsOldestAdvancesAtDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started, release := make(chan string, 3), make(chan struct{})
	q := newIDWorkQueue(ctx, 2, 1, func(ctx context.Context, id string) {
		started <- id
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	other := newIDWorkQueue(ctx, 1, 1, func(context.Context, string) {})
	t.Cleanup(func() { cancel(); q.Wait(); other.Wait() })
	reg := prometheus.NewPedanticRegistry()
	registerIDWorkQueueMetrics(reg, q, other)
	if !q.Enqueue("active") {
		t.Fatal("first notification rejected")
	}
	waitForQueueItem(t, started, "active")
	if !q.Enqueue("older") || !q.Enqueue("younger") {
		t.Fatal("waiting notifications rejected")
	}
	q.mu.Lock()
	q.pending["older"] = time.Unix(1700000000, 0)
	q.pending["younger"] = time.Unix(1700000010, 0)
	q.mu.Unlock()
	requireNotificationSamples(t, reg, "build", map[string]float64{"waiting/": 2, "active/": 1, "oldest_enqueued_timestamp_seconds/": 1700000000})
	release <- struct{}{}
	waitForQueueItem(t, started, "older")
	requireNotificationSamples(t, reg, "build", map[string]float64{"waiting/": 1, "active/": 1, "oldest_enqueued_timestamp_seconds/": 1700000010})
	release <- struct{}{}
	waitForQueueItem(t, started, "younger")
	requireNotificationSamples(t, reg, "build", map[string]float64{"waiting/": 0, "active/": 1, "oldest_enqueued_timestamp_seconds/": 0})
}

func TestIDWorkQueueMetricsScrapeDoesNotHoldQueueLock(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	q := newIDWorkQueue(ctx, 1, 1, func(ctx context.Context, _ string) { <-ctx.Done() })
	other := newIDWorkQueue(ctx, 1, 1, func(context.Context, string) {})
	t.Cleanup(func() { cancel(); q.Wait(); other.Wait() })
	reg := prometheus.NewPedanticRegistry()
	registerIDWorkQueueMetrics(reg, q, other)
	samples := make(chan prometheus.Metric)
	done := make(chan struct{})
	go func() { reg.Collect(samples); close(samples); close(done) }()
	// Receive one sample, then leave the unbuffered consumer blocked. Queue
	// admission must work even while the scrape cannot emit its next sample.
	select {
	case <-samples:
	case <-time.After(3 * time.Second):
		t.Fatal("collector failed to produce a sample")
	}
	t.Cleanup(func() {
		for range samples {
		}
		<-done
	})
	accepted := make(chan bool, 1)
	go func() { accepted <- q.Enqueue("a") }()
	select {
	case ok := <-accepted:
		if !ok {
			t.Fatal("admission failed during blocked scrape")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("collector held queue mutex while emitting samples")
	}
}

func TestIDWorkQueueMetricsDrainAndMinimums(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	q := newIDWorkQueue(ctx, 0, 0, func(context.Context, string) {})
	other := newIDWorkQueue(ctx, 2, 2, func(context.Context, string) {})
	t.Cleanup(func() { cancel(); q.Wait(); other.Wait() })
	reg := prometheus.NewPedanticRegistry()
	registerIDWorkQueueMetrics(reg, q, other)
	if !q.Enqueue("a") {
		t.Fatal("notification rejected")
	}
	deadline := time.After(3 * time.Second)
	for q.snapshot().waiting != 0 || q.snapshot().active != 0 {
		select {
		case <-deadline:
			t.Fatal("handler did not drain")
		case <-time.After(time.Millisecond):
		}
	}
	// No waiting ID means zero oldest timestamp, even after real work.
	requireNotificationSamples(t, reg, "build", map[string]float64{"waiting/": 0, "active/": 0, "capacity/": 1, "workers/": 1, "enabled/": 1, "oldest_enqueued_timestamp_seconds/": 0})
	cancel()
	if err := q.WaitContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestIDWorkQueueMetricsShutdownDoesNotClaimDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	q := newIDWorkQueue(ctx, 1, 1, func(context.Context, string) {
		once.Do(func() { close(entered) })
		<-release // Deliberately ignores cancellation, like a broken driver.
	})
	other := newIDWorkQueue(ctx, 1, 1, func(context.Context, string) {})
	t.Cleanup(func() { cancel(); close(release); q.Wait(); other.Wait() })
	reg := prometheus.NewPedanticRegistry()
	registerIDWorkQueueMetrics(reg, q, other)
	if !q.Enqueue("active") {
		t.Fatal("notification rejected")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}
	if !q.Enqueue("waiting") {
		t.Fatal("waiting notification rejected")
	}
	cancel()
	waitCtx, stopWait := context.WithCancel(t.Context())
	stopWait()
	if err := q.WaitContext(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitContext = %v, want canceled", err)
	}
	requireNotificationSamples(t, reg, "build", map[string]float64{"enabled/": 0, "active/": 1, "waiting/": 1})
}
