package managedpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type idleTailTestProvider struct {
	*usageTestProvider
	tail    IdleTail
	err     error
	windows []UsageWindow
	// background counts reads made under WithBackgroundRequests.
	background int
}

func (p *idleTailTestProvider) IdleTail(ctx context.Context, providerResourceID string, window UsageWindow, maxRequests int) (IdleTail, error) {
	if providerResourceID != "provider-db" || maxRequests != idleTailRequestsPerRead {
		return IdleTail{}, ErrInvalid
	}
	if BackgroundRequests(ctx) {
		p.background++
	}
	p.windows = append(p.windows, window)
	return p.tail, p.err
}

func idleTailFixture(t *testing.T, now time.Time, provider Provider, spec Spec, databases int) (*MemoryStore, *Registry) {
	t.Helper()
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
			MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	for i := range databases {
		id := "db" + string(rune('a'+i))
		store.databases[id] = Database{ID: id, AccountID: "account", Name: "orders-" + id, State: StateReady,
			Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
			ProviderResourceID: "provider-db", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now}
	}
	return store, registry
}

// The sweep collects two windows of catch-up for each database; only the
// latest is observed, after collection, and a correction-only sweep observes
// nothing.
func TestUsageCollectorObservesIdleTailOfTheLatestNewWindow(t *testing.T) {
	alwaysOn := testSpec()
	alwaysOn.ScaleToZero = false
	readings := func(compute int64) []MeterReading {
		return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: compute}}
	}
	tests := []struct {
		name     string
		spec     Spec
		compute  int64
		err      error
		plain    bool
		want     []IdleTailObservation
		wantRead int
	}{
		{name: "read", spec: testSpec(), compute: 600, wantRead: 2, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailRead, ComputeUnitSeconds: 600, Tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90, Requests: 2}},
			{Class: ClassDevelopment, Outcome: IdleTailRead, ComputeUnitSeconds: 600, Tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90, Requests: 2}},
		}},
		{name: "no compute needs no read", spec: testSpec(), compute: 0, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailNoCompute}, {Class: ClassDevelopment, Outcome: IdleTailNoCompute},
		}},
		// The first sweep's pass ends at the failure; the next sweep's pass
		// reaches the window left queued.
		{name: "a failed read ends the pass without failing collection", spec: testSpec(), compute: 600, err: ErrUnavailable, wantRead: 2, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailUnavailable, ComputeUnitSeconds: 600},
			{Class: ClassDevelopment, Outcome: IdleTailUnavailable, ComputeUnitSeconds: 600},
		}},
		{name: "an unsupported database does not end the pass", spec: testSpec(), compute: 600, err: ErrUnsupported, wantRead: 2, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailUnsupported, ComputeUnitSeconds: 600},
			{Class: ClassDevelopment, Outcome: IdleTailUnsupported, ComputeUnitSeconds: 600},
		}},
		{name: "unsupported provider", spec: testSpec(), compute: 600, plain: true, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailUnsupported, ComputeUnitSeconds: 600},
			{Class: ClassDevelopment, Outcome: IdleTailUnsupported, ComputeUnitSeconds: 600},
		}},
		{name: "always-on is not observed", spec: alwaysOn, compute: 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 11, 24, 12, 17, 0, 0, time.UTC)
			usage := &usageTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, readings: readings(tt.compute)}
			tailing := &idleTailTestProvider{usageTestProvider: usage, tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90, Requests: 2}, err: tt.err}
			var provider Provider = tailing
			if tt.plain {
				provider = usage
			}
			store, registry := idleTailFixture(t, now, provider, tt.spec, 2)
			var got []IdleTailObservation
			// One collector, as Run keeps: windows a pass does not reach stay
			// queued for the next sweep.
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now },
				ObserveIdleTail: func(observation IdleTailObservation) { got = append(got, observation) }})
			if err != nil {
				t.Fatal(err)
			}
			collect := func() {
				t.Helper()
				if _, err := collector.Collect(ctx); err != nil {
					t.Fatal(err)
				}
				collector.readIdleTails(ctx)
			}
			collect()
			// The next sweep in the same hour replays corrections only.
			now = now.Add(time.Minute)
			collect()
			if len(got) != len(tt.want) {
				t.Fatalf("observations = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("observation %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
			if len(tailing.windows) != tt.wantRead || tailing.background != tt.wantRead {
				t.Fatalf("idle tail reads = %+v, want %d", tailing.windows, tt.wantRead)
			}
			latest := now.Truncate(time.Hour)
			for _, window := range tailing.windows {
				if !window.To.Equal(latest) {
					t.Fatalf("read a catch-up window %+v, want only the one ending %s", window, latest)
				}
			}
		})
	}
}

func TestMetricsIdleTailSharesWindowsWithCompute(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "apid", true)
	if err != nil {
		t.Fatal(err)
	}
	metrics.ObserveIdleTail(IdleTailObservation{Class: ClassProduction, Outcome: IdleTailRead, ComputeUnitSeconds: 3600, Tail: IdleTail{Suspends: 2, ComputeUnitSeconds: 240}})
	metrics.ObserveIdleTail(IdleTailObservation{Class: ClassProduction, Outcome: IdleTailNoCompute})
	// A failed read's compute would inflate the denominator.
	metrics.ObserveIdleTail(IdleTailObservation{Class: ClassProduction, Outcome: IdleTailUnavailable, ComputeUnitSeconds: 9999})
	metrics.ObserveIdleTail(IdleTailObservation{Class: "unknown", Outcome: IdleTailRead, ComputeUnitSeconds: 9999})
	for _, check := range []struct {
		collector prometheus.Collector
		want      float64
	}{
		{metrics.idleTailComputeTotal.WithLabelValues("production"), 3600},
		{metrics.idleTailSuspends.WithLabelValues("production"), 2},
		{metrics.idleTailTotal.WithLabelValues("production"), 240},
		{metrics.idleTailReadsTotal.WithLabelValues(IdleTailRead), 1},
		{metrics.idleTailReadsTotal.WithLabelValues(IdleTailNoCompute), 1},
		{metrics.idleTailReadsTotal.WithLabelValues(IdleTailUnavailable), 1},
	} {
		if got := testutil.ToFloat64(check.collector); got != check.want {
			t.Fatalf("%v = %v, want %v", check.collector, got, check.want)
		}
	}
	var nilMetrics *Metrics
	nilMetrics.ObserveIdleTail(IdleTailObservation{})
}
