package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestUsageDailyPointsAggregatesTopAppAndSorts(t *testing.T) {
	dayOne := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	dayTwo := dayOne.AddDate(0, 0, 1)
	rows := []state.DailyUsage{
		{AppID: "app-b", Day: dayTwo, MBSeconds: 3_600_000},
		{AppID: "app-a", Day: dayOne, MBSeconds: 7_200_000},
		{AppID: "app-b", Day: dayOne, MBSeconds: 3_600_000},
	}
	apps := []state.App{
		{ID: "app-a", Slug: "api"},
		{ID: "app-b", Slug: "worker"},
	}

	got := usageDailyPoints(rows, apps)
	if len(got) != 2 {
		t.Fatalf("got %+v, want two daily points", got)
	}
	if got[0].Date != "2026-09-01" || got[0].GBHours != 2.9296875 || got[0].TopAppSlug != "api" || got[0].TopAppGBHours != 1.953125 {
		t.Fatalf("day one = %+v", got[0])
	}
	if got[1].Date != "2026-09-02" || got[1].GBHours != 0.9765625 || got[1].TopAppSlug != "worker" {
		t.Fatalf("day two = %+v", got[1])
	}
}
