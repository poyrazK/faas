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
}

func (p *idleTailTestProvider) IdleTail(_ context.Context, providerResourceID string, window UsageWindow) (IdleTail, error) {
	if providerResourceID != "provider-db" {
		return IdleTail{}, ErrNotFound
	}
	p.windows = append(p.windows, window)
	return p.tail, p.err
}

func idleTailFixture(t *testing.T, now time.Time, provider Provider, spec Spec) (*MemoryStore, *Registry) {
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
	store.databases["db"] = Database{ID: "db", AccountID: "account", Name: "orders", State: StateReady,
		Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ProviderResourceID: "provider-db", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now}
	return store, registry
}

func TestUsageCollectorObservesIdleTailOncePerNewWindow(t *testing.T) {
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
			{Class: ClassDevelopment, Outcome: IdleTailRead, ComputeUnitSeconds: 600, Tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90}},
			{Class: ClassDevelopment, Outcome: IdleTailRead, ComputeUnitSeconds: 600, Tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90}},
		}},
		{name: "no compute needs no read", spec: testSpec(), compute: 0, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailNoCompute}, {Class: ClassDevelopment, Outcome: IdleTailNoCompute},
		}},
		{name: "read failure does not fail collection", spec: testSpec(), compute: 600, err: ErrUnavailable, wantRead: 2, want: []IdleTailObservation{
			{Class: ClassDevelopment, Outcome: IdleTailUnavailable, ComputeUnitSeconds: 600},
			{Class: ClassDevelopment, Outcome: IdleTailUnavailable, ComputeUnitSeconds: 600},
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
			tailing := &idleTailTestProvider{usageTestProvider: usage, tail: IdleTail{Suspends: 3, ComputeUnitSeconds: 90}, err: tt.err}
			var provider Provider = tailing
			if tt.plain {
				provider = usage
			}
			store, registry := idleTailFixture(t, now, provider, tt.spec)
			var got []IdleTailObservation
			collect := func() {
				t.Helper()
				collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now },
					ObserveIdleTail: func(observation IdleTailObservation) { got = append(got, observation) }})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := collector.Collect(ctx); err != nil {
					t.Fatal(err)
				}
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
			if len(tailing.windows) != tt.wantRead {
				t.Fatalf("idle tail reads = %+v, want %d", tailing.windows, tt.wantRead)
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
