package neon

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func usageFrame(from, to time.Time, metrics ...map[string]any) map[string]any {
	return map[string]any{"timeframe_start": from, "timeframe_end": to, "metrics": metrics}
}

func usagePeriod(from time.Time, frames ...map[string]any) map[string]any {
	return map[string]any{"period_id": "period-a", "period_start": from, "consumption": frames}
}

func TestUsageRequiresCompleteConsumptionCoverage(t *testing.T) {
	from := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	middle, to := from.Add(time.Hour), from.Add(2*time.Hour)
	metric := map[string]any{"metric_name": "compute_unit_seconds", "value": 3}
	for _, test := range []struct {
		name    string
		periods []map[string]any
		cursor  string
		wantErr bool
		compute int64
	}{
		{name: "normal project cursor", periods: []map[string]any{usagePeriod(from, usageFrame(from, middle, metric), usageFrame(middle, to, metric))}, cursor: "quiet-river-123", compute: 6},
		{name: "omitted zero meters", periods: []map[string]any{usagePeriod(from, usageFrame(from, to))}},
		{name: "no periods", wantErr: true},
		{name: "no timeframes", periods: []map[string]any{usagePeriod(from)}, wantErr: true},
		{name: "missing boundaries", periods: []map[string]any{usagePeriod(from, map[string]any{"metrics": []map[string]any{metric}})}, wantErr: true},
		{name: "missing last hour", periods: []map[string]any{usagePeriod(from, usageFrame(from, middle, metric))}, wantErr: true},
		{name: "missing first hour", periods: []map[string]any{usagePeriod(from, usageFrame(middle, to, metric))}, wantErr: true},
		{name: "outside requested window", periods: []map[string]any{usagePeriod(from, usageFrame(from.Add(-time.Hour), to, metric))}, wantErr: true},
		{name: "reversed boundaries", periods: []map[string]any{usagePeriod(from, usageFrame(to, from, metric))}, wantErr: true},
		{name: "duplicate timeframe", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, metric), usageFrame(from, to, metric))}, wantErr: true},
		{name: "overlapping timeframes", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, metric), usageFrame(middle, to, metric))}, wantErr: true},
		{name: "duplicate meter", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, metric, metric))}, wantErr: true},
		{name: "missing meter value", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": "compute_unit_seconds"}))}, wantErr: true},
		{name: "null meter value", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": "compute_unit_seconds", "value": nil}))}, wantErr: true},
		{name: "unrequested meter", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": "extra_branches_month", "value": 3}))}, wantErr: true},
		{name: "negative meter", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": "compute_unit_seconds", "value": -3}))}, wantErr: true},
		{name: "overflowing compute", periods: []map[string]any{usagePeriod(from, usageFrame(from, middle, map[string]any{"metric_name": "compute_unit_seconds", "value": math.MaxInt64}), usageFrame(middle, to, metric))}, wantErr: true},
		{name: "duplicate period", periods: []map[string]any{usagePeriod(from, usageFrame(from, to, metric)), usagePeriod(from, usageFrame(from, to, metric))}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("project_ids") != "quiet-river-123" || r.URL.Query().Get("limit") != "1" {
					t.Errorf("usage project filter = %v", r.URL.Query())
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"projects": []map[string]any{{"project_id": "quiet-river-123", "periods": test.periods}}, "pagination": map[string]any{"cursor": test.cursor}})
			}))
			usage, err := provider.Usage(context.Background(), "quiet-river-123", managedpostgres.UsageWindow{From: from, To: to})
			if test.wantErr {
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatalf("incomplete or duplicate consumption accepted: %+v, %v", usage, err)
				}
				return
			}
			if err != nil || len(usage.Readings) != 4 || usage.Readings[0].Quantity != test.compute {
				t.Fatalf("complete usage = %+v, %v", usage, err)
			}
		})
	}
}

func TestUsageCoversBillingPeriodChangeWithinTimeframe(t *testing.T) {
	from := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	to, change := from.Add(time.Hour), from.Add(30*time.Minute)
	first := usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": "compute_unit_seconds", "value": 3}))
	first["period_end"] = change
	second := usagePeriod(change, usageFrame(from, to, map[string]any{"metric_name": "compute_unit_seconds", "value": 4}))
	second["period_id"] = "period-b"
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, http.StatusOK, map[string]any{"projects": []map[string]any{{"project_id": "quiet-river-123", "periods": []map[string]any{second, first}}}})
	}))
	usage, err := provider.Usage(context.Background(), "quiet-river-123", managedpostgres.UsageWindow{From: from, To: to})
	if err != nil || len(usage.Readings) != 4 || usage.Readings[0].Quantity != 7 {
		t.Fatalf("usage across billing change = %+v, %v", usage, err)
	}
}

func TestUsageNormalizesByteMonthStorage(t *testing.T) {
	from := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	const secondsPerBillingMonth = int64(744 * 3600)
	for _, metric := range []string{"root_branch_bytes_month", "child_branch_bytes_month", "instant_restore_bytes_month", "snapshot_storage_bytes_month"} {
		t.Run(metric, func(t *testing.T) {
			for _, quantity := range []int64{1_000_000_000, math.MaxInt64 / secondsPerBillingMonth, math.MaxInt64/secondsPerBillingMonth + 1} {
				provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					period := usagePeriod(from, usageFrame(from, to, map[string]any{"metric_name": metric, "value": quantity}))
					writeResponse(t, w, http.StatusOK, map[string]any{"projects": []map[string]any{{"project_id": "quiet-river-123", "periods": []map[string]any{period}}}})
				}))
				usage, err := provider.Usage(context.Background(), "quiet-river-123", managedpostgres.UsageWindow{From: from, To: to})
				if quantity > math.MaxInt64/secondsPerBillingMonth {
					if !errors.Is(err, managedpostgres.ErrUnavailable) {
						t.Fatalf("overflowing byte-months accepted: %+v, %v", usage, err)
					}
					continue
				}
				index := 1
				if metric == "instant_restore_bytes_month" || metric == "snapshot_storage_bytes_month" {
					index = 2
				}
				if err != nil || len(usage.Readings) != 4 || usage.Readings[index].Quantity != quantity*secondsPerBillingMonth {
					t.Fatalf("byte-month normalization: %+v, %v; want %d byte-seconds", usage, err, quantity*secondsPerBillingMonth)
				}
			}
		})
	}
}
