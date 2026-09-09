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
	sender, err := New(Config{Kind: KindHTTPJSON, TargetURL: server.URL, MaxAttempts: 2, OnDelivered: func(Record) { delivered <- struct{}{} }})
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
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retry")
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
