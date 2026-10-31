// adr: 20261025094915 — the cap sweep records each threshold a subject reaches
// once per UTC month, and only while caps are enforced.
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestCapThresholds(t *testing.T) {
	const allowance = int64(10000)
	for _, tc := range []struct {
		name    string
		used    int64
		limits  CapLimits
		ceiling int64
		want    []string
	}{
		{"under 80%", 7999, CapLimits{Allowance: allowance, Ceiling: allowance}, allowance, nil},
		{"80% warns", 8000, CapLimits{Allowance: allowance, Ceiling: allowance}, allowance, []string{CapThresholdWarning}},
		{"the allowance without overage suspends", allowance, CapLimits{Allowance: allowance, Ceiling: allowance}, allowance,
			[]string{CapThresholdWarning, CapThresholdAllowance, CapThresholdSuspended}},
		{"past the allowance with overage", 12000, CapLimits{Allowance: allowance, Ceiling: 2 * allowance, Overage: true}, 2 * allowance,
			[]string{CapThresholdWarning, CapThresholdAllowance}},
		{"80% of the ceiling with overage", 16000, CapLimits{Allowance: allowance, Ceiling: 2 * allowance, Overage: true}, 2 * allowance,
			[]string{CapThresholdWarning, CapThresholdAllowance, CapThresholdCeilingWarning}},
		{"the ceiling with overage", 20000, CapLimits{Allowance: allowance, Ceiling: 2 * allowance, Overage: true}, 2 * allowance,
			[]string{CapThresholdWarning, CapThresholdAllowance, CapThresholdCeilingWarning, CapThresholdSuspended}},
		{"an operator cap below the allowance suspends first", 5000, CapLimits{Allowance: allowance, Ceiling: allowance}, 5000,
			[]string{CapThresholdSuspended}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := capThresholds(tc.used, tc.limits, tc.ceiling); !slices.Equal(got, tc.want) {
				t.Fatalf("thresholds %v; want %v", got, tc.want)
			}
		})
	}
}

// runCapSweep runs one sweep over one ready database whose subject has a
// 10,000 CU-s allowance and no overage.
func runCapSweep(t *testing.T, mode CapEnforcementMode, store *MemoryStore, used int64, now time.Time, notify func(context.Context, CapEvent) error) CapSweepSummary {
	t.Helper()
	month := monthStart(now)
	provider := &capTestProvider{
		fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		observed:     map[string]ComputeCapObservation{"proj-a": {Scope: "a", PeriodStart: month, UsedUnitSeconds: used, QuotaUnitSeconds: 1}},
		set:          map[string]int64{},
	}
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
			MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store.databases["db-a"] = Database{ID: "db-a", AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ProviderResourceID: "proj-a", State: StateReady, CreatedAt: month, UpdatedAt: month}
	enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
		Mode: mode, Now: func() time.Time { return now }, Qualification: testCapQualification(backend), Notify: notify,
		Ceiling: func(context.Context, string) (CapLimits, error) {
			return CapLimits{Allowance: 10000, Ceiling: 10000}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := enforcer.Enforce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestCapEnforcerRecordsEachThresholdOncePerMonth(t *testing.T) {
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(20 * 24 * time.Hour)
	run := func(t *testing.T, mode CapEnforcementMode, store *MemoryStore, used int64) CapSweepSummary {
		t.Helper()
		return runCapSweep(t, mode, store, used, now, nil)
	}

	t.Run("shadow records nothing", func(t *testing.T) {
		store := NewMemoryStore()
		if summary := run(t, CapEnforcementShadow, store, 10000); summary.EventsRecorded != 0 || len(store.capEvents) != 0 {
			t.Fatalf("shadow recorded %d events", len(store.capEvents))
		}
	})
	t.Run("on records each threshold once", func(t *testing.T) {
		store := NewMemoryStore()
		if summary := run(t, CapEnforcementOn, store, 8500); summary.EventsRecorded != 1 {
			t.Fatalf("80%% sweep recorded %d", summary.EventsRecorded)
		}
		if summary := run(t, CapEnforcementOn, store, 10000); summary.EventsRecorded != 2 {
			t.Fatalf("ceiling sweep recorded %d; want the allowance and the suspension, not the warning again", summary.EventsRecorded)
		}
		if summary := run(t, CapEnforcementOn, store, 10500); summary.EventsRecorded != 0 {
			t.Fatalf("a repeat sweep recorded %d", summary.EventsRecorded)
		}
		at, err := store.CapSuspendedAt(t.Context(), "acct-a", now)
		if err != nil || at == nil || !at.Equal(now) {
			t.Fatalf("suspended at %v, %v; want %v", at, err, now)
		}
		if next, _ := store.CapSuspendedAt(t.Context(), "acct-a", month.AddDate(0, 1, 0)); next != nil {
			t.Fatalf("suspension carried into the next month: %v", next)
		}
	})
}

func TestCapEnforcerMailsTheMostSevereThresholdOnce(t *testing.T) {
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(20 * 24 * time.Hour)
	var mailed []string
	failNext := false
	notify := func(_ context.Context, event CapEvent) error {
		if failNext {
			failNext = false
			return errors.New("outbox unavailable")
		}
		mailed = append(mailed, event.Threshold)
		return nil
	}
	store := NewMemoryStore()
	// The outbox links the intent; the memory store stands in for it.
	linking := func(ctx context.Context, event CapEvent) error {
		if err := notify(ctx, event); err != nil {
			return err
		}
		store.markCapEventMailed(event)
		return nil
	}

	if summary := runCapSweep(t, CapEnforcementShadow, store, 10000, now, linking); len(mailed) != 0 || summary.NotifyFailed != 0 {
		t.Fatalf("shadow mailed %v", mailed)
	}
	runCapSweep(t, CapEnforcementOn, store, 8500, now, linking)
	if !slices.Equal(mailed, []string{CapThresholdWarning}) {
		t.Fatalf("80%% sweep mailed %v", mailed)
	}
	runCapSweep(t, CapEnforcementOn, store, 8600, now, linking)
	if len(mailed) != 1 {
		t.Fatalf("a repeat sweep mailed again: %v", mailed)
	}
	failNext = true
	if summary := runCapSweep(t, CapEnforcementOn, store, 10000, now, linking); summary.NotifyFailed != 1 || len(mailed) != 1 {
		t.Fatalf("failed email: summary %+v, mailed %v", summary, mailed)
	}
	runCapSweep(t, CapEnforcementOn, store, 10000, now.Add(time.Hour), linking)
	if !slices.Equal(mailed, []string{CapThresholdWarning, CapThresholdSuspended}) {
		t.Fatalf("retry mailed %v; want one suspension email, not the allowance", mailed)
	}

	stale := NewMemoryStore()
	runCapSweep(t, CapEnforcementOn, stale, 10000, now, nil)
	mailed = nil
	runCapSweep(t, CapEnforcementOn, stale, 10000, now.Add(capMailWindow), linking)
	if len(mailed) != 0 {
		t.Fatalf("a day-old threshold was mailed: %v", mailed)
	}
}
