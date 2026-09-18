package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 046
func TestUsageAppDataUsesCanonicalInterfaceEgress(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	got := usageAppData(
		[]state.Usage{{AppID: "app-a", TXBytes: gib, NetTxBytes: 2 * gib}},
		[]state.App{{ID: "app-a", Slug: "api"}},
		0,
	)
	if len(got) != 1 || got[0].EgressGB != 2 {
		t.Fatalf("usage app egress = %+v, want canonical 2 GB", got)
	}
}

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

func TestUsageDailyRowsForMonthExcludesAdjacentMonths(t *testing.T) {
	rows := []state.DailyUsage{
		{AppID: "august", Day: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)},
		{AppID: "september-start", Day: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{AppID: "september-end", Day: time.Date(2026, 9, 30, 23, 59, 59, 0, time.FixedZone("UTC+3", 3*60*60))},
		{AppID: "october", Day: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}

	got := usageDailyRowsForMonth(rows, time.Date(2026, 9, 18, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60)))
	if len(got) != 2 || got[0].AppID != "september-start" || got[1].AppID != "september-end" {
		t.Fatalf("got %+v, want only September UTC rows", got)
	}
}
