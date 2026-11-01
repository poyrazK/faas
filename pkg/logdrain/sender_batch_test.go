package logdrain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A durable Datadog or OTLP drain sends its backlog in batches. It used to
// POST one line per request, one at a time, so a drain managed about one
// line per round trip and a chatty app filled its spool.
func TestDurableSenderBatchesDatadogAndOTLP(t *testing.T) {
	for _, kind := range []Kind{KindDatadog, KindOTLP} {
		t.Run(string(kind), func(t *testing.T) {
			var requests, lines atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				switch kind {
				case KindDatadog:
					var logs []map[string]any
					_ = json.Unmarshal(body, &logs)
					lines.Add(int64(len(logs)))
				case KindOTLP:
					var payload otlpLogsPayload
					_ = json.Unmarshal(body, &payload)
					for _, resource := range payload.ResourceLogs {
						for _, scope := range resource.ScopeLogs {
							lines.Add(int64(len(scope.LogRecords)))
						}
					}
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-batch"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{Kind: kind, TargetURL: server.URL, DurableQueue: q}
			if kind == KindDatadog {
				cfg.TargetURL = "https://http-intake.logs.datadoghq.com/api/v2/logs"
				cfg.AuthHeader = "DD-API-KEY: k"
			}
			sender, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if kind == KindDatadog {
				sender.cfg.TargetURL = server.URL // the intake allow-list is checked at New
			}
			for i := 1; i <= 50; i++ {
				if !sender.Enqueue(Record{InstanceID: "vm-1", Sequence: uint64(i), Line: "line", WrittenAt: time.Unix(int64(i), 0)}) {
					t.Fatal("enqueue refused")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go sender.Run(ctx)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && lines.Load() < 50 {
				time.Sleep(10 * time.Millisecond)
			}
			if lines.Load() != 50 {
				t.Fatalf("delivered %d lines, want 50", lines.Load())
			}
			if requests.Load() > 2 {
				t.Fatalf("%d requests for 50 queued lines, want them batched", requests.Load())
			}
			if stats := q.Stats(); stats.PendingRecords != 0 {
				t.Fatalf("queue still holds %d records after delivery", stats.PendingRecords)
			}
		})
	}
}

// The retry window runs from a batch's first failure, not from enqueue: a
// backlog older than the window used to be dead-lettered after three quick
// attempts on one transient error.
func TestDurableSenderRetryWindowStartsAtFirstFailure(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 4 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-window"})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := New(Config{Kind: KindOTLP, TargetURL: server.URL, DurableQueue: q, MaxAttempts: 3, RetryWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// Enqueued long ago: a backlog older than the retry window.
	if err := q.Enqueue(Record{InstanceID: "vm-1", Sequence: 1, Line: "old"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, err := q.NextBatch(10, deliveryBatchBytes)
	if err != nil || len(items) != 1 {
		t.Fatalf("NextBatch = %d, %v", len(items), err)
	}
	if err := sender.deliverDurableBatch(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if stats := q.Stats(); stats.DeadLetterTotal != 0 || stats.PendingRecords != 0 {
		t.Fatalf("stats = %+v, want delivered after the 429s, nothing dead-lettered", stats)
	}
}

// Delivered cursors of instances that sent nothing for lastSequenceTTL are
// pruned; the cursor file used to gain one entry per instance forever.
func TestQueuePrunesStaleInstanceCursors(t *testing.T) {
	q, err := NewQueue(QueueConfig{Root: t.TempDir(), DrainID: "drain-prune"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	q.state.LastSequences = map[string]uint64{"gone": 9, "fresh": 3}
	q.state.LastSeen = map[string]int64{"gone": now.Add(-lastSequenceTTL - time.Hour).Unix(), "fresh": now.Unix()}
	if err := q.Enqueue(Record{InstanceID: "live", Sequence: 1, Line: "x"}, now); err != nil {
		t.Fatal(err)
	}
	items, err := q.NextBatch(1, deliveryBatchBytes)
	if err != nil || len(items) != 1 {
		t.Fatalf("NextBatch = %d, %v", len(items), err)
	}
	if err := q.AckBatch(items); err != nil {
		t.Fatal(err)
	}
	got := q.LastSequences()
	if _, ok := got["gone"]; ok {
		t.Fatalf("stale cursor kept: %v", got)
	}
	if got["fresh"] != 3 || got["live"] != 1 {
		t.Fatalf("cursors = %v, want fresh and live kept", got)
	}
}
