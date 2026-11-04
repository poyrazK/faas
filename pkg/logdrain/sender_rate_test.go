// adr: 20261104085150 — one log drain delivers at most its byte budget; a
// record past it is dropped and counted, as a full outbox's is.
package logdrain

import (
	"strings"
	"testing"
	"time"
)

func TestSenderByteRateDropsPastTheBudget(t *testing.T) {
	var dropped int
	s, err := New(Config{Kind: KindHTTPJSON, TargetURL: "https://logs.example.com/ingest",
		BytesPerSecond: 100, BurstBytes: 250, QueueSize: 16,
		OnDropped: func(Record) { dropped++ }})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	line := Record{Line: strings.Repeat("x", 100)}
	accepted := 0
	for range 4 {
		if s.Enqueue(line) {
			accepted++
		}
	}
	if accepted != 2 || dropped != 2 {
		t.Fatalf("burst: accepted %d dropped %d; want 2 and 2", accepted, dropped)
	}
	// One second refills 100 bytes: one more line.
	now = now.Add(time.Second)
	if !s.Enqueue(line) || s.Enqueue(line) {
		t.Fatal("after one second want exactly one more line accepted")
	}
}

func TestSenderWithoutByteRateAcceptsUpToTheQueue(t *testing.T) {
	s, err := New(Config{Kind: KindHTTPJSON, TargetURL: "https://logs.example.com/ingest", QueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		if !s.Enqueue(Record{Line: strings.Repeat("x", 1<<20)}) {
			t.Fatalf("record %d refused without a byte budget", i)
		}
	}
}
