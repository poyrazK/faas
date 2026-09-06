package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPercentileMSNearestRank(t *testing.T) {
	values := []int{900, 100, 500, 200}
	if got := percentileMS(values, 0.50); got != 200 {
		t.Fatalf("p50 = %d, want 200", got)
	}
	if got := percentileMS(values, 0.95); got != 900 {
		t.Fatalf("p95 = %d, want 900", got)
	}
	if got := percentileMS(nil, 0.50); got != 0 {
		t.Fatalf("empty percentile = %d, want 0", got)
	}
}

func TestWakeRecommendationTierBackCompat(t *testing.T) {
	for _, tc := range []struct {
		row  api.WakeTimelineJSONRow
		want string
	}{
		{row: api.WakeTimelineJSONRow{Tier: "warm"}, want: "warm"},
		{row: api.WakeTimelineJSONRow{Method: "restore"}, want: "init"},
		{row: api.WakeTimelineJSONRow{Method: "cold_boot"}, want: "cold_boot_fallback"},
		{row: api.WakeTimelineJSONRow{Method: ""}, want: ""},
	} {
		if got := wakeRecommendationTier(tc.row); got != tc.want {
			t.Errorf("tier for %#v = %q, want %q", tc.row, got, tc.want)
		}
	}
}
