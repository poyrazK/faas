package managedpostgres

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestReadIdleTailsSpendsARequestBudgetAndKeepsTheRestQueued(t *testing.T) {
	usage := &usageTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	tailing := &idleTailTestProvider{usageTestProvider: usage, tail: IdleTail{Requests: 2}}
	var outcomes []string
	collector := &UsageCollector{logger: slog.Default(), idleTailObserver: func(observation IdleTailObservation) {
		outcomes = append(outcomes, observation.Outcome)
	}}
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	pending := func(id string, window UsageWindow) pendingIdleTail {
		return pendingIdleTail{
			database: Database{ID: id, ProviderResourceID: "provider-db", Spec: testSpec()},
			backend:  Backend{Provider: tailing},
			usage:    Usage{Window: window, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 60}}},
		}
	}
	window := UsageWindow{From: from, To: from.Add(time.Hour)}
	for i := range 40 {
		collector.pendingIdleTails = append(collector.pendingIdleTails, pending(string(rune('A'+i)), window))
	}
	// Two requests a read: 29 reads leave 2 of 60, under one read's ceiling.
	collector.readIdleTails(context.Background())
	if len(tailing.windows) != 29 || len(outcomes) != 29 || len(collector.pendingIdleTails) != 11 {
		t.Fatalf("first pass: reads = %d, outcomes = %d, queued = %d", len(tailing.windows), len(outcomes), len(collector.pendingIdleTails))
	}
	// A newer window replaces a database's queued one, which is skipped.
	next := UsageWindow{From: window.To, To: window.To.Add(time.Hour)}
	queuedID := collector.pendingIdleTails[0].database.ID
	latest := pending(queuedID, next).usage
	collector.queueIdleTails([]*usageCollectionWork{{database: collector.pendingIdleTails[0].database, backend: Backend{Provider: tailing}, latest: &latest}})
	if len(outcomes) != 30 || outcomes[29] != IdleTailSkipped || len(collector.pendingIdleTails) != 11 ||
		!collector.pendingIdleTails[0].usage.Window.To.Equal(next.To) {
		t.Fatalf("replace: outcomes = %v, queued = %d", outcomes, len(collector.pendingIdleTails))
	}
	collector.readIdleTails(context.Background())
	if len(tailing.windows) != 40 || len(collector.pendingIdleTails) != 0 {
		t.Fatalf("second pass: reads = %d, queued = %d", len(tailing.windows), len(collector.pendingIdleTails))
	}
}
