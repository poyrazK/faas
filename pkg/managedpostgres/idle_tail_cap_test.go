package managedpostgres

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestReadIdleTailsCapsProviderReadsPerSweep(t *testing.T) {
	usage := &usageTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	tailing := &idleTailTestProvider{usageTestProvider: usage}
	var outcomes []string
	collector := &UsageCollector{logger: slog.Default(), idleTailObserver: func(observation IdleTailObservation) {
		outcomes = append(outcomes, observation.Outcome)
	}}
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	window := UsageWindow{From: from, To: from.Add(time.Hour)}
	for range maximumIdleTailReadsPerSweep + 1 {
		collector.pendingIdleTails = append(collector.pendingIdleTails, pendingIdleTail{
			database: Database{ProviderResourceID: "provider-db", Spec: testSpec()},
			backend:  Backend{Provider: tailing},
			usage:    Usage{Window: window, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 60}}},
		})
	}
	collector.readIdleTails(context.Background())
	if len(tailing.windows) != maximumIdleTailReadsPerSweep || len(outcomes) != maximumIdleTailReadsPerSweep+1 ||
		outcomes[len(outcomes)-1] != IdleTailSkipped || collector.pendingIdleTails != nil {
		t.Fatalf("reads = %d, outcomes = %v", len(tailing.windows), outcomes)
	}
}
