package state_test

// adr: 20261031144636 — a custom metric keeps its newest observation: a value
// stamped before the stored one is dropped. OTLP deltas add to a counter's
// total, and a delta for a name that held a gauge restarts from the delta.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type customMetricWriter interface {
	state.CustomMetricKindStore
	ListCustomMetrics(ctx context.Context, appID string) ([]state.CustomMetric, error)
}

func checkCustomMetricWrites(t *testing.T, ctx context.Context, s customMetricWriter, appID string) {
	t.Helper()
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	read := func(name string) state.CustomMetric {
		t.Helper()
		rows, err := s.ListCustomMetrics(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Name == name {
				return row
			}
		}
		t.Fatalf("no %s in %+v", name, rows)
		return state.CustomMetric{}
	}

	if err := s.PutCustomMetricOfKind(ctx, appID, "queue", state.CustomMetricKindGauge, 10, at, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCustomMetricOfKind(ctx, appID, "queue", state.CustomMetricKindGauge, 3, at.Add(-time.Minute), 5); err != nil {
		t.Fatalf("an older value is dropped, not refused: %v", err)
	}
	if got := read("queue"); got.Value != 10 || !got.ObservedAt.Equal(at) {
		t.Fatalf("queue = %+v; want the newer 10 kept", got)
	}

	for i, delta := range []float64{4, 6} {
		if err := s.AddCustomMetricCounter(ctx, appID, "checkouts", delta, at.Add(time.Duration(i)*time.Minute), 5); err != nil {
			t.Fatal(err)
		}
	}
	// An older delta still counts; the total's time stays the newest.
	if err := s.AddCustomMetricCounter(ctx, appID, "checkouts", 2, at.Add(-time.Hour), 5); err != nil {
		t.Fatal(err)
	}
	if got := read("checkouts"); got.Value != 12 || got.Kind != state.CustomMetricKindCounter || !got.ObservedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("checkouts = %+v; want a counter total of 12 at the newest time", got)
	}
	if err := s.AddCustomMetricCounter(ctx, appID, "queue", 5, at.Add(time.Hour), 5); err != nil {
		t.Fatal(err)
	}
	if got := read("queue"); got.Value != 5 || got.Kind != state.CustomMetricKindCounter {
		t.Fatalf("queue after a delta = %+v; want a counter restarted at 5", got)
	}

	for _, name := range []string{"c", "d", "e"} {
		if err := s.AddCustomMetricCounter(ctx, appID, name, 1, at, 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddCustomMetricCounter(ctx, appID, "f", 1, at, 5); err != state.ErrCustomMetricLimit {
		t.Fatalf("a sixth name = %v; want ErrCustomMetricLimit", err)
	}
	if err := s.PutCustomMetricOfKind(ctx, appID, "f", state.CustomMetricKindGauge, 1, at, 5); err != state.ErrCustomMetricLimit {
		t.Fatalf("a sixth gauge = %v; want ErrCustomMetricLimit", err)
	}
}

func TestMemStoreCustomMetricWrites(t *testing.T) {
	checkCustomMetricWrites(t, context.Background(), state.NewMemStore(), uuid.NewString())
}

func TestPgStore_CustomMetricWrites(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	checkCustomMetricWrites(t, ctx, s, appID)
}
