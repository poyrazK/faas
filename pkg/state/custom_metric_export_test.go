package state_test

// adr: 20261104142830 — the exporter's read keeps a counter however long ago
// it last moved, so a sparse counter's next increment is not lost to a
// series that vanished; a stale gauge is still left out.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type customMetricExporterStore interface {
	state.CustomMetricKindStore
	state.CustomMetricExportStore
}

func checkCounterStaysExported(t *testing.T, ctx context.Context, s customMetricExporterStore, appID string) {
	t.Helper()
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if err := s.AddCustomMetricCounter(ctx, appID, "payment_failures", 1, old, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCustomMetricOfKind(ctx, appID, "queue_depth", state.CustomMetricKindGauge, 3, old, 5); err != nil {
		t.Fatal(err)
	}
	samples, err := s.ListFreshCustomMetrics(ctx, time.Now().UTC().Add(-5*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, sample := range samples {
		if sample.AppID == appID {
			got[sample.Name] = sample.Value
		}
	}
	if v, ok := got["payment_failures"]; !ok || v != 1 {
		t.Fatalf("exported = %v; want the idle counter at 1", got)
	}
	if _, ok := got["queue_depth"]; ok {
		t.Fatalf("exported = %v; a stale gauge must stay out", got)
	}
}

func TestMemStoreCounterStaysExported(t *testing.T) {
	checkCounterStaysExported(t, context.Background(), state.NewMemStore(), uuid.NewString())
}

func TestPgStore_CounterStaysExported(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	checkCounterStaysExported(t, ctx, s, appID)
}
