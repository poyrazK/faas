package meter

// adr: 046

// usage_summary_test.go — pkg/meter/BuildAppWindowSummary unit tests.
//
// The handler-level tests live in cmd/apid/handlers_usage_test.go
// and exercise the full stack (plan gate, DTO shape, overage math).
// These unit tests pin down the helper's data-only contract:
//   - empty window (since ≥ until) → zero summary, no SQL round-trip
//   - single-day window sums to one Usage row's worth
//   - multi-day window sums across rows
//   - rows for other apps under the same account are filtered out
//   - builder_seconds is CPUHours(cpu_usec) * 3600 (i.e., CPU-seconds)

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// stubStore implements UsageSummaryStore for unit tests. Mirrors the
// shape of state.Store.UsageByHour's return without spinning up the
// full state.Store surface.
type stubStore struct {
	rows []state.Usage
	err  error
}

func (s stubStore) UsageByHour(_ context.Context, _ string, _, _ time.Time) ([]state.Usage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func TestBuildAppWindowSummary_EmptyWindow(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	store := stubStore{rows: []state.Usage{{AppID: "app-1", MBSeconds: 999}}}

	sum, source, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1", now, now)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "usage_minutes" {
		t.Errorf("source = %q, want usage_minutes (even on empty window)", source)
	}
	if sum.MBSeconds != 0 || sum.GBHours != 0 {
		t.Errorf("empty window: got %+v, want zero", sum)
	}
}

func TestBuildAppWindowSummary_StoreError(t *testing.T) {
	want := errors.New("pg down")
	store := stubStore{err: want}
	_, _, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1",
		time.Now().Add(-1*time.Hour), time.Now())
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestBuildAppWindowSummary_FiltersToOneApp(t *testing.T) {
	now := time.Now().UTC()
	rows := []state.Usage{
		{AppID: "app-1", MBSeconds: 1024, Requests: 100, TXBytes: 2048, NetTxBytes: 8192, CPUUsec: 1_000_000, ColdBootCount: 1},
		{AppID: "app-2", MBSeconds: 999999, Requests: 99999, TXBytes: 99999, NetTxBytes: 99999, CPUUsec: 9_999_999, ColdBootCount: 9},
		{AppID: "app-1", MBSeconds: 2048, Requests: 200, TXBytes: 4096, NetTxBytes: 16384, CPUUsec: 2_000_000, ColdBootCount: 2},
	}
	store := stubStore{rows: rows}

	sum, _, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1",
		now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// app-1 totals: MBSeconds=3072, Requests=300, TXBytes=6144, ColdBootCount=3
	if sum.MBSeconds != 3072 {
		t.Errorf("MBSeconds = %d, want 3072 (app-2 row must NOT leak in)", sum.MBSeconds)
	}
	if sum.Requests != 300 {
		t.Errorf("Requests = %d, want 300", sum.Requests)
	}
	if sum.TxBytes != 6144 {
		t.Errorf("TxBytes = %d, want 6144", sum.TxBytes)
	}
	if sum.NetTxBytes != 24576 {
		t.Errorf("NetTxBytes = %d, want 24576", sum.NetTxBytes)
	}
	if sum.ColdBootCount != 3 {
		t.Errorf("ColdBootCount = %d, want 3", sum.ColdBootCount)
	}
	// GBHours = 3072 / 1024 / 3600 ≈ 0.000833
	if sum.GBHours <= 0 || sum.GBHours >= 1 {
		t.Errorf("GBHours = %v, want ~0.0008 (3072 mb-s in one hour)", sum.GBHours)
	}
	// BuilderSeconds = CPUHours(3_000_000) * 3600 = (3M / 3.6B) * 3600 ≈ 3.0
	if sum.BuilderSeconds < 2.99 || sum.BuilderSeconds > 3.01 {
		t.Errorf("BuilderSeconds = %v, want ~3.0 (3M µs = 3 CPU-seconds)", sum.BuilderSeconds)
	}
}

func TestBuildAppWindowSummary_GBHoursRounds(t *testing.T) {
	// 1024 mb-seconds = exactly 1/3600 GB-hours ≈ 0.000278.
	// Pin the rounding discipline so the wire field matches the
	// financial-model cells (MonthlyUsageGB rounding precedent).
	rows := []state.Usage{{AppID: "app-1", MBSeconds: 1024}}
	store := stubStore{rows: rows}

	sum, _, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Round to 6 dp: 0.000278
	if sum.GBHours < 0.0002 || sum.GBHours > 0.0003 {
		t.Errorf("GBHours = %v, want ~0.000278 (6dp rounding)", sum.GBHours)
	}
}

func TestBuildAppWindowSummary_SourceIsUsageMinutes(t *testing.T) {
	rows := []state.Usage{{AppID: "app-1"}}
	store := stubStore{rows: rows}

	_, source, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if source != "usage_minutes" {
		t.Errorf("source = %q, want usage_minutes (today's rollup reader)", source)
	}
}

// spec: §4.7
// TestBuildAppWindowSummary_FirstUsageHour pins the savings clamp
// input: the earliest hour with non-zero MBSeconds for the app, in
// UTC, ignoring other apps and zero-RAM rows (e.g. request-only
// hours). An app that billed nothing reports the zero time.
func TestBuildAppWindowSummary_FirstUsageHour(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := stubStore{rows: []state.Usage{
		{AppID: "app-1", Month: day.Add(5 * time.Hour), MBSeconds: 10},
		{AppID: "app-2", Month: day.Add(1 * time.Hour), MBSeconds: 10},
		{AppID: "app-1", Month: day.Add(2 * time.Hour), Requests: 3},
		{AppID: "app-1", Month: day.Add(3 * time.Hour), MBSeconds: 10},
	}}
	sum, _, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-1", day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := day.Add(3 * time.Hour); !sum.FirstUsageHour.Equal(want) {
		t.Errorf("FirstUsageHour = %v, want %v", sum.FirstUsageHour, want)
	}

	idle, _, err := BuildAppWindowSummary(context.Background(), store, "acct-1", "app-3", day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !idle.FirstUsageHour.IsZero() {
		t.Errorf("idle app FirstUsageHour = %v, want zero", idle.FirstUsageHour)
	}
}
