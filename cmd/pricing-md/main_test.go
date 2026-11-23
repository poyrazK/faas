package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderUsesEveryPlanAndCanonicalValues(t *testing.T) {
	out := render()
	_, comparison, _ := strings.Cut(out, "| Price and limits |")
	comparison, _, _ = strings.Cut(comparison, "\n\n")
	lines := strings.Split(comparison, "\n")
	if len(lines) != 13 {
		t.Fatalf("expected one header, one separator and eleven comparison rows, got %d lines", len(lines))
	}
	header := strings.Split(lines[0], "|")
	if len(header) != len(api.Plans)+1 {
		t.Fatalf("unexpected plan columns: %s", lines[0])
	}
	for col, plan := range api.Plans {
		if got := strings.TrimSpace(header[col]); got != titlePlan(plan) {
			t.Errorf("column %d: got plan %q, want %q", col, got, titlePlan(plan))
		}
		limits, ok := api.LimitsFor(plan)
		if !ok {
			t.Fatalf("plan %q missing from limits table", plan)
		}
		for row, want := range []string{
			formatPrice(limits.PriceMillicents), fmt.Sprint(limits.DeployedApps),
			fmt.Sprint(limits.PreviewApps), fmt.Sprint(limits.DeveloperApps), fmt.Sprint(limits.MaxConcurrency),
			formatCount(limits.RAMMB), formatCount(limits.IncludedGBHours), formatThousands(limits.MonthlyEgressGiB),
			formatCount(limits.AppLayerMaxMB), formatDuration(limits.IdleTimeoutS),
			formatDuration(int(limits.RequestBudgetMaxDuration().Seconds())),
		} {
			cells := strings.Split(lines[row+2], "|")
			if len(cells) != len(api.Plans)+3 {
				t.Fatalf("unexpected comparison row: %s", lines[row+2])
			}
			if got := strings.TrimSpace(cells[col+2]); got != want {
				t.Errorf("%s %s: got %q, want %q", plan, strings.TrimSpace(cells[1]), got, want)
			}
		}
		leaseRow := fmt.Sprintf("| **%s** | %d | %dh |", titlePlan(plan), limits.DeveloperApps, limits.DeveloperLeaseMaxHours)
		if !strings.Contains(out, leaseRow) {
			t.Errorf("plan %q missing developer lease row %q", plan, leaseRow)
		}
	}
	for plan, row := range map[api.Plan]string{
		api.PlanFree:  "| **Free** | 1 GiB | stops at 1 GiB | 1 GiB | 10 GiB |",
		api.PlanScale: "| **Scale** | 200 GiB | $0.03 per GiB-month | 1,000 GiB | 2,000 GiB |",
	} {
		if !strings.Contains(out, row) {
			t.Errorf("plan %q missing object-storage row %q", plan, row)
		}
	}
	// ErrPlanLimitDeveloperApps/Lease link to this anchor.
	if !strings.Contains(out, "## Developer environments") {
		t.Errorf("render missing the developer-environments section")
	}
	if !strings.Contains(out, "$0.03 per GiB, up to the [spend cap]") {
		t.Errorf("render missing the egress overage price")
	}
}

func TestFormatExactPrice(t *testing.T) {
	for millicents, want := range map[int64]string{23_310: "$0.2331", 9_450: "$0.0945", 50_000: "$0.50", 21_000: "$0.21", 10_500: "$0.105", 100_000: "$1.00"} {
		if got := formatExactPrice(millicents); got != want {
			t.Errorf("formatExactPrice(%d) = %q, want %q", millicents, got, want)
		}
	}
}

func TestFormatPrice(t *testing.T) {
	for _, tc := range []struct {
		millicents int64
		want       string
	}{
		{0, "$0"},
		{1_000_000, "$10"},
		{1_234_000, "$12.34"},
	} {
		if got := formatPrice(tc.millicents); got != tc.want {
			t.Errorf("formatPrice(%d) = %q, want %q", tc.millicents, got, tc.want)
		}
	}
}
