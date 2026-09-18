package main

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// usageDailyRowsForMonth limits the trailing daily rollup returned by the
// store to the UTC calendar month requested by the usage summary endpoint.
// Without this boundary filter, an early-month summary includes rows from the
// previous month even though its totals and Month field describe only the
// requested month.
func usageDailyRowsForMonth(rows []state.DailyUsage, month time.Time) []state.DailyUsage {
	month = month.UTC()
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	out := make([]state.DailyUsage, 0, len(rows))
	for _, row := range rows {
		day := row.Day.UTC()
		if !day.Before(start) && day.Before(end) {
			out = append(out, row)
		}
	}
	return out
}

// usageDailyPoints folds per-app daily rollup rows into the account-level
// series exposed by GET /v1/usage/summary. The sort is deliberately done here
// as well as in the store so alternate Store implementations cannot change
// the wire ordering. Ties use app ID as a stable fallback.
func usageDailyPoints(rows []state.DailyUsage, apps []state.App) []api.DailyUsagePoint {
	slugByApp := make(map[string]string, len(apps))
	for _, app := range apps {
		slugByApp[app.ID] = app.Slug
	}

	type aggregate struct {
		point  api.DailyUsagePoint
		topApp string
	}
	byDate := make(map[string]*aggregate)
	for _, row := range rows {
		date := row.Day.UTC().Format("2006-01-02")
		gbHours := meter.GBHours(row.MBSeconds)
		item := byDate[date]
		if item == nil {
			item = &aggregate{point: api.DailyUsagePoint{Date: date}}
			byDate[date] = item
		}
		item.point.GBHours += gbHours
		if gbHours > item.point.TopAppGBHours ||
			(gbHours == item.point.TopAppGBHours && row.AppID < item.topApp) {
			item.point.TopAppGBHours = gbHours
			item.topApp = row.AppID
			item.point.TopAppSlug = slugByApp[row.AppID]
		}
	}

	out := make([]api.DailyUsagePoint, 0, len(byDate))
	for _, item := range byDate {
		out = append(out, item.point)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
