package faas

// adr: 20261113180000

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeQueueServer struct {
	mu      sync.Mutex
	pending []string // payloads still to lease
	settled map[string]string
	nackErr string
	leases  int
}

func (f *fakeQueueServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	var body struct {
		Receipts []string `json:"receipts"`
		Error    string   `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case strings.HasSuffix(path, "/leases"):
		f.leases++
		messages := []map[string]any{}
		for _, payload := range f.pending {
			messages = append(messages, map[string]any{"id": payload, "receipt": "r-" + payload,
				"payload": json.RawMessage(`"` + payload + `"`), "attempt": 1, "enqueued_at": time.Now(), "lease_expires_at": time.Now().Add(time.Minute)})
		}
		f.pending = nil
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
	case strings.HasSuffix(path, ":ack"), strings.HasSuffix(path, ":nack"):
		verb := path[strings.LastIndex(path, ":")+1:]
		for _, receipt := range body.Receipts {
			f.settled[receipt] = verb
		}
		if verb == "nack" {
			f.nackErr = body.Error
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"succeeded": body.Receipts, "failed": []any{}})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"succeeded": body.Receipts, "failed": []any{}})
	}
}

func TestConsumeQueueAcksAndNacks(t *testing.T) {
	fake := &fakeQueueServer{pending: []string{"good", "bad"}, settled: map[string]string{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handled := make(chan struct{}, 2)
	go func() {
		<-handled
		<-handled
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err = client.ConsumeQueue(ctx, QueueConsumerOptions{App: "worker", Queue: "jobs", Concurrency: 2, Wait: time.Second},
		func(_ context.Context, msg QueueLeaseMessage) error {
			defer func() { handled <- struct{}{} }()
			if msg.ID == "bad" {
				return errors.New("boom")
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.settled["r-good"] != "ack" || fake.settled["r-bad"] != "nack" || fake.nackErr != "boom" {
		t.Fatalf("settled = %v, nack error %q", fake.settled, fake.nackErr)
	}
	// The empty answers after the batch are paced, not spun.
	if fake.leases > 10 {
		t.Fatalf("%d lease calls; empty answers were not paced", fake.leases)
	}
}

func TestTruncateQueueErrorKeepsUTF8(t *testing.T) {
	text := strings.Repeat("a", queueConsumerErrorBytes-1) + "é" + "tail"
	got := truncateQueueError(text)
	if len(got) > queueConsumerErrorBytes || !strings.HasPrefix(text, got) || strings.HasSuffix(got, "\xc3") {
		t.Fatalf("truncated to %d bytes ending %q", len(got), got[len(got)-2:])
	}
}
