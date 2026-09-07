package main

import (
	"bytes"
	"strings"
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

func TestRenderTriggerClassSummary(t *testing.T) {
	var out bytes.Buffer
	prev := osStdout
	osStdout = &out
	defer func() { osStdout = prev }()

	renderTriggerClassSummary(api.AppWakeTimelineResponse{
		Rows:                  []api.WakeTimelineJSONRow{{TriggerClass: "monitor"}, {TriggerClass: "crawler"}, {TriggerClass: "user"}},
		TriggerClassHistogram: map[string]int{"monitor": 1, "crawler": 1},
	}, api.AppResponse{RAMMB: 512, IdleTimeoutS: 60})
	got := out.String()
	if !strings.Contains(got, "2 of your last 3 wakes were monitors/crawlers") {
		t.Fatalf("summary = %q", got)
	}
	if !strings.Contains(got, "≈ €") {
		t.Fatalf("summary missing cost estimate: %q", got)
	}
}

func TestEstimateCrawlerWakeCostEUR(t *testing.T) {
	if got := estimateCrawlerWakeCostEUR(0, api.AppResponse{RAMMB: 512, IdleTimeoutS: 60}); got != 0 {
		t.Fatalf("zero wakes cost = %v, want 0", got)
	}
	if got := estimateCrawlerWakeCostEUR(1, api.AppResponse{RAMMB: 512, IdleTimeoutS: 60}); got <= 0 {
		t.Fatalf("one wake cost = %v, want positive", got)
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
