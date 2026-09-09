package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCheckTXT_WildcardUsesZoneChallengeName(t *testing.T) {
	old := txtLookupFunc
	t.Cleanup(func() { txtLookupFunc = old })
	var got string
	txtLookupFunc = func(_ context.Context, target string) ([]string, error) {
		got = target
		return []string{"token"}, nil
	}
	if !checkTXT(context.Background(), "*.example.com", "token") {
		t.Fatal("wildcard TXT token was not accepted")
	}
	if got != "_faas-verify.example.com" || strings.Contains(got, "*") {
		t.Fatalf("TXT lookup target = %q, want zone owner", got)
	}
}

// TestMemStore_OldestDoctorObservation (ADR-120 Tier A1) walks
// the three observable states: empty table → zero time.Time;
// single row → that row's observed_at; many rows → the minimum
// observed_at. Mirrors the MIN(observed_at) SQL path in
// PgStore.OldestDoctorObservation.
func TestMemStore_OldestDoctorObservation(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()

	// Cold start: no rows.
	got, err := m.OldestDoctorObservation(ctx)
	if err != nil {
		t.Fatalf("cold start: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("cold start: want zero time.Time, got %v", got)
	}

	// Seed three observations with observed_at strictly increasing.
	now := time.Now().UTC()
	if err := m.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{
		Domain: "a.example.com", ObservedAt: now.Add(-3 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := m.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{
		Domain: "b.example.com", ObservedAt: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if err := m.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{
		Domain: "c.example.com", ObservedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert c: %v", err)
	}

	got, err = m.OldestDoctorObservation(ctx)
	if err != nil {
		t.Fatalf("three rows: %v", err)
	}
	want := now.Add(-3 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("three rows: want %v, got %v", want, got)
	}
}

// TestEmitDoctorOldestObservationGauge (ADR-120 Tier A1) exercises
// the dns_poller gauge-emission path end-to-end against a MemStore
// + a real wire.NewOpsMetrics registry. Asserts the gauge surfaces
// in /metrics with the expected Set() values across the three
// observable states (cold start → 0; row present → age > 0).
func TestEmitDoctorOldestObservationGauge(t *testing.T) {
	srv := &server{store: state.NewMemStore(), ops: wire.NewOpsMetrics("apid_test")}
	ctx := context.Background()
	log := slog.Default()

	// Cold start: gauge should be 0 (Set explicitly so /metrics
	// surfaces a zero-valued series).
	srv.emitDoctorOldestObservationGauge(ctx, log)
	if got := testutil.ToFloat64(srv.ops.DomainDoctorOldestObservationSeconds()); got != 0 {
		t.Fatalf("cold start: want gauge=0, got %v", got)
	}

	// Seed one row, advance the wall clock past the observed_at
	// by 90s. The gauge should read ~90s (allow ±2s slack for
	// the time.Since call between Set and the test assertion).
	now := time.Now().UTC()
	if err := srv.store.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{
		Domain: "api.example.com", ObservedAt: now.Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	srv.emitDoctorOldestObservationGauge(ctx, log)
	got := testutil.ToFloat64(srv.ops.DomainDoctorOldestObservationSeconds())
	if got < 88 || got > 92 {
		t.Fatalf("one row 90s old: want gauge in [88,92], got %v", got)
	}

	// Clock-skew safety: a row whose observed_at is in the future
	// (e.g. Postgres clock ahead of the apid host) must clamp to 0
	// so the alert never sees a negative age. Uses a fresh
	// MemStore so the prior api.example.com row (now-90s, still
	// the MIN) doesn't dominate — review-fix #7 caught the
	// prior test seeding a future row on top of an older row,
	// leaving the clamp branch never entered.
	futureSrv := &server{store: state.NewMemStore(), ops: wire.NewOpsMetrics("apid_test_clock_skew")}
	if err := futureSrv.store.UpsertDoctorObservation(ctx, state.DomainDoctorObservation{
		Domain: "future.example.com", ObservedAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert future: %v", err)
	}
	futureSrv.emitDoctorOldestObservationGauge(ctx, log)
	if got := testutil.ToFloat64(futureSrv.ops.DomainDoctorOldestObservationSeconds()); got != 0 {
		t.Fatalf("future observed_at only: want gauge=0 (clamp), got %v", got)
	}
}

// TestEmitDoctorSkipNilSafe (ADR-120 Tier A1) asserts the
// skipped_flag_disabled helper is nil-safe — a server constructed
// without ops (MemStore-only test harness) must not panic.
func TestEmitDoctorSkipNilSafe(t *testing.T) {
	srv := &server{}
	log := slog.Default()
	srv.emitDoctorSkip(log) // must not panic on nil s.ops
}
