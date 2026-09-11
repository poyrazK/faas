package logdrain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSenderHTTPJSONDelivery(t *testing.T) {
	record := Record{AppID: "app-1", AccountID: "acct-1", InstanceID: "vm-1", Sequence: 7, Stream: "stdout", Line: "hello", WrittenAt: time.Unix(10, 20).UTC()}
	got := make(chan httpRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body Record
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		got <- httpRequest{method: r.Method, contentType: r.Header.Get("Content-Type"), auth: r.Header.Get("Authorization"), body: body}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sender, err := New(Config{Kind: KindHTTPJSON, TargetURL: server.URL, AuthHeader: "Authorization: Bearer token", QueueSize: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx)
	if !sender.Enqueue(record) {
		t.Fatal("Enqueue returned false")
	}
	select {
	case request := <-got:
		if request.method != http.MethodPost || request.contentType != "application/json" || request.auth != "Bearer token" {
			t.Fatalf("request metadata = %+v", request)
		}
		if request.body != record {
			t.Fatalf("body = %+v, want %+v", request.body, record)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

func TestSenderOTLPDelivery(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		body, _ = io.ReadAll(r.Body)
		bodyCh <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	sender, err := New(Config{Kind: KindOTLP, TargetURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx)
	sender.Enqueue(Record{AppID: "app-1", AccountID: "acct-1", InstanceID: "vm-1", Sequence: 1, Line: "line", WrittenAt: time.Unix(1, 2)})
	select {
	case body := <-bodyCh:
		payload := string(body)
		for _, fragment := range []string{`"resourceLogs"`, `"logRecords"`, `"faas.app.id"`, `"line"`} {
			if !strings.Contains(payload, fragment) {
				t.Fatalf("OTLP payload missing %q: %s", fragment, payload)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OTLP delivery")
	}
}

func TestSenderQueueDropIsObservable(t *testing.T) {
	var dropped atomic.Int64
	sender, err := New(Config{Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", QueueSize: 1, OnDropped: func(Record) { dropped.Add(1) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !sender.Enqueue(Record{Line: "first"}) || sender.Enqueue(Record{Line: "second"}) {
		t.Fatal("queue did not exhibit bounded non-blocking behavior")
	}
	if dropped.Load() != 1 {
		t.Fatalf("dropped = %d, want 1", dropped.Load())
	}
}

func TestSenderRetriesTransientHTTPFailure(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	delivered := make(chan struct{}, 1)
	retries := make(chan int, 1)
	latency := make(chan time.Duration, 1)
	sender, err := New(Config{
		Kind: KindHTTPJSON, TargetURL: server.URL, MaxAttempts: 2,
		OnDelivered:        func(Record) { delivered <- struct{}{} },
		OnRetry:            func(_ Record, attempt int) { retries <- attempt },
		OnDeliveredLatency: func(_ Record, d time.Duration) { latency <- d },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx)
	sender.Enqueue(Record{Line: "retry"})
	select {
	case <-delivered:
		if attempts.Load() != 2 {
			t.Fatalf("attempts = %d, want 2", attempts.Load())
		}
		select {
		case attempt := <-retries:
			if attempt != 2 {
				t.Fatalf("retry attempt = %d, want 2", attempt)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for retry callback")
		}
		select {
		case d := <-latency:
			if d <= 0 {
				t.Fatalf("delivery latency = %s, want positive", d)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for delivery latency callback")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retry")
	}
}

func TestSenderQueueTelemetry(t *testing.T) {
	depths := make(chan [2]int, 4)
	sender, err := New(Config{
		Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", QueueSize: 2,
		OnQueueDepth: func(depth, capacity int) { depths <- [2]int{depth, capacity} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	select {
	case got := <-depths:
		if got != [2]int{0, 2} {
			t.Fatalf("initial queue telemetry = %v, want [0 2]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial queue telemetry")
	}
	if !sender.Enqueue(Record{Line: "queued"}) {
		t.Fatal("Enqueue returned false")
	}
	select {
	case got := <-depths:
		if got != [2]int{1, 2} {
			t.Fatalf("enqueue queue telemetry = %v, want [1 2]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for enqueue queue telemetry")
	}
}

func TestSenderShutdownDropsQueuedRecords(t *testing.T) {
	dropped := make(chan Record, 3)
	sender, err := New(Config{
		Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", QueueSize: 2,
		OnDropped: func(record Record) { dropped <- record },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !sender.Enqueue(Record{Sequence: 1}) || !sender.Enqueue(Record{Sequence: 2}) {
		t.Fatal("failed to queue records")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		sender.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sender did not stop")
	}
	if sender.Enqueue(Record{Sequence: 3}) {
		t.Fatal("enqueue after shutdown unexpectedly succeeded")
	}
	if got := len(dropped); got != 3 {
		t.Fatalf("dropped records = %d, want 3", got)
	}
}

func TestSenderRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{
		{Kind: "vendor", TargetURL: "https://logs.example.test"},
		{Kind: KindHTTPJSON, TargetURL: "file:///tmp/logs"},
		{Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", AuthHeader: "Authorization"},
		{Kind: KindHTTPJSON, TargetURL: "https://logs.example.test", AuthHeader: "Host: evil"},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("New(%+v) unexpectedly succeeded", cfg)
		}
	}
}

type httpRequest struct {
	method      string
	contentType string
	auth        string
	body        Record
}
