package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderUsesEveryPlanAndCanonicalValues(t *testing.T) {
	out := render()
	for _, plan := range api.Plans {
		if !strings.Contains(out, "**"+titlePlan(plan)+"**") {
			t.Fatalf("render missing plan %q:\n%s", plan, out)
		}
		limits, ok := api.LimitsFor(plan)
		if !ok {
			t.Fatalf("plan %q missing from limits table", plan)
		}
		for _, want := range []string{formatPrice(limits.PriceMillicents),
			fmtInt(limits.DeployedApps), fmtInt(limits.DeveloperApps),
			fmtInt(limits.MaxConcurrency),
			fmtInt(limits.RAMMB), fmtInt(limits.IncludedGBHours),
			fmtInt(limits.AppLayerMaxMB)} {
			if !strings.Contains(out, want) {
				t.Errorf("plan %q missing canonical value %q", plan, want)
			}
		}
	}
}

func TestFormatPrice(t *testing.T) {
	for _, tc := range []struct {
		millicents int64
		want       string
	}{
		{0, "€0"},
		{900_000, "€9"},
		{1_234_000, "€12.34"},
	} {
		if got := formatPrice(tc.millicents); got != tc.want {
			t.Errorf("formatPrice(%d) = %q, want %q", tc.millicents, got, tc.want)
		}
	}
}

func fmtInt(v int) string { return fmt.Sprintf("%d", v) }
