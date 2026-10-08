package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEurosFromMillicentsRoundsHalfUp(t *testing.T) {
	for millicents, want := range map[int64]string{
		0: "€0.00", 499: "€0.00", 500: "€0.01", 23_867: "€0.24", 100_000: "€1.00", 1_234_567: "€12.35", -1_500: "-€0.02",
	} {
		if got := euros(millicents); got != want {
			t.Errorf("euros(%d) = %q, want %q", millicents, got, want)
		}
	}
}

func TestRenderUsageSavingsShowsFiguresAndMethodology(t *testing.T) {
	var buf bytes.Buffer
	renderUsageSavings(&buf, api.AppSavingsResponse{
		Slug:        "my-api",
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		AlwaysOnGBHours: 24.375, ActualGBHours: 0.507813, SavedGBHours: 23.867188,
		AlwaysOnMillicents: 24_375, ActualMillicents: 508, SavedMillicents: 23_867,
		ParkedRatio: 0.979167, Methodology: "Estimate: billed RAM-time compared with always-on.",
	})
	out := buf.String()
	for _, want := range []string{"my-api", "2026-09-01 → 2026-09-03", "23.867 GB-hours (€0.24)", "parked 98% of the time", "Estimate:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
