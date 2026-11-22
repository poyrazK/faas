package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const wakeRecommendationWindow = 7 * 24 * time.Hour

type wakeRecommendationStats struct {
	ready []int
}

func renderWakeRecommendation(ctx context.Context, client *api.Client, slug string, app api.AppResponse) {
	timeline, complete, err := wakeRecommendationHistory(ctx, client, slug, time.Now().UTC())
	if err != nil || len(timeline.Rows) < 10 {
		if err == nil && len(timeline.Rows) > 0 {
			renderTriggerClassSummary(timeline, app)
		}
		return
	}
	renderTriggerClassSummary(timeline, app)

	stats := make(map[string]*wakeRecommendationStats)
	for _, row := range timeline.Rows {
		if row.ReadyInMS <= 0 {
			continue
		}
		tier := wakeRecommendationTier(row)
		if tier == "" {
			continue
		}
		if stats[tier] == nil {
			stats[tier] = &wakeRecommendationStats{}
		}
		stats[tier].ready = append(stats[tier].ready, int(row.ReadyInMS))
	}

	if complete {
		_, _ = fmt.Fprintf(osStdout, "wake recommendation (last 7d, %d wakes):\n", len(timeline.Rows))
	} else {
		// The history holds more wakes than one read covers: say which
		// ones the figures describe instead of calling them the last 7 days.
		_, _ = fmt.Fprintf(osStdout, "wake recommendation (newest %d wakes of the last 7d):\n", len(timeline.Rows))
	}
	for _, tier := range []string{"warm", "init", "cold_boot_fallback"} {
		stat := stats[tier]
		if stat == nil || len(stat.ready) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "  %s: p50=%dms p95=%dms (n=%d)\n",
			tier, percentileMS(stat.ready, 0.50), percentileMS(stat.ready, 0.95), len(stat.ready))
	}

	projectedWarm := 0.0
	if stat := stats["warm"]; stat != nil && len(stat.ready) > 0 {
		projectedWarm = float64(percentileMS(stat.ready, 0.50))
	} else if stat := stats["init"]; stat != nil && len(stat.ready) > 0 {
		// Warm snapshots are designed to resume in at most half the
		// init-snapshot p50 (pkg/api/limits.go / ADR-074).
		projectedWarm = float64(percentileMS(stat.ready, 0.50)) * 0.5
	}
	if projectedWarm > 0 {
		_, _ = fmt.Fprintf(osStdout, "  projected warm p50: %.0fms\n", projectedWarm)
	} else {
		_, _ = fmt.Fprintln(osStdout, "  projected warm p50: n/a")
	}

	if acct, err := client.Whoami(ctx); err == nil {
		plan := api.Plan(acct.Plan)
		if cost := ResidentGBHoursPerMonth(plan, app.RAMMB, 1); cost > 0 {
			_, _ = fmt.Fprintf(osStdout, "  min 1 monthly resident: %s\n", formatGBHours(cost))
		} else {
			_, _ = fmt.Fprintf(osStdout, "  min 1 monthly resident: unavailable on %s plan\n", acct.Plan)
		}
	}
}

// renderTriggerClassSummary makes known non-user wake cost visible beside the
// existing latency recommendation. The estimate assumes one idle timeout per
// suppressed wake and uses the platform's published overage rate; it is a
// planning number, not an invoice calculation.
func renderTriggerClassSummary(timeline api.AppWakeTimelineResponse, app api.AppResponse) {
	wakes := len(timeline.Rows)
	if wakes == 0 {
		return
	}
	known := timeline.TriggerClassHistogram["monitor"] + timeline.TriggerClassHistogram["crawler"]
	if known == 0 {
		for _, row := range timeline.Rows {
			if row.TriggerClass == "monitor" || row.TriggerClass == "crawler" {
				known++
			}
		}
	}
	_, _ = fmt.Fprintf(osStdout,
		"  %d of your last %d wakes were monitors/crawlers (≈ $%.2f estimated resident cost)\n",
		known, wakes, estimateCrawlerWakeCost(known, app))
}

func estimateCrawlerWakeCost(wakes int, app api.AppResponse) float64 {
	if wakes <= 0 || app.RAMMB <= 0 || app.IdleTimeoutS <= 0 {
		return 0
	}
	gbHours := float64(app.RAMMB+api.PerVMOverheadMB) * float64(wakes) * float64(app.IdleTimeoutS) / (1024 * 3600)
	return gbHours * float64(api.OverageMillicentsPerGBHour) / 100000
}

func wakeRecommendationTier(row api.WakeTimelineJSONRow) string {
	switch row.Tier {
	case "warm", "init", "cold_boot_fallback":
		return row.Tier
	}
	// Older telemetry has method but no tier. Keep those rows useful
	// during the additive rollout by mapping restore/cold_boot to the
	// corresponding non-warm tier.
	switch row.Method {
	case "restore":
		return "init"
	case "cold_boot":
		return "cold_boot_fallback"
	default:
		return ""
	}
}

func percentileMS(values []int, quantile float64) int {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]int(nil), values...)
	sort.Ints(ordered)
	idx := int(math.Ceil(float64(len(ordered)) * quantile))
	if idx < 1 {
		idx = 1
	}
	if idx > len(ordered) {
		idx = len(ordered)
	}
	return ordered[idx-1]
}

// wakeRecommendationMaxPages bounds the 7-day history read to
// wakeRecommendationMaxPages * api.WakeHistoryPageMax wakes.
const wakeRecommendationMaxPages = 10

// wakeRecommendationHistory reads the last seven days of wakes through the
// keyset pages and reports whether it covered the whole window. The windowed
// wake-timeline read returns only the 100 newest instances, so the
// recommendation printed "last 7d" over a fraction of a busy app's wakes
// (Observe audit 2026-11-21, finding 5). A server without paged history falls
// back to that read, reported as incomplete when it hit its bound.
func wakeRecommendationHistory(ctx context.Context, client *api.Client, slug string, now time.Time) (api.AppWakeTimelineResponse, bool, error) {
	since, until := now.Add(-wakeRecommendationWindow).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	merged := api.AppWakeTimelineResponse{TriggerHistogram: map[string]int{}, TriggerClassHistogram: map[string]int{}}
	cursor := ""
	for page := 0; page < wakeRecommendationMaxPages; page++ {
		resp, err := client.GetAppWakeHistoryPage(ctx, slug, api.AppWakeHistoryPageOptions{Since: since, Until: until, Cursor: cursor, Limit: api.WakeHistoryPageMax})
		if err != nil || resp.Page == nil {
			if page > 0 {
				return merged, false, nil
			}
			legacy, lerr := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{Since: since, Until: until})
			return legacy, lerr == nil && len(legacy.Rows) < api.WakeHistoryPageMax, lerr
		}
		merged.App, merged.AsOf = resp.App, resp.AsOf
		merged.Rows = append(merged.Rows, resp.Rows...)
		for k, v := range resp.TriggerHistogram {
			merged.TriggerHistogram[k] += v
		}
		for k, v := range resp.TriggerClassHistogram {
			merged.TriggerClassHistogram[k] += v
		}
		merged.WakeCount24h += resp.WakeCount24h
		merged.WakeCountWithMeta += resp.WakeCountWithMeta
		merged.AtCapacityCount += resp.AtCapacityCount
		if resp.Page.Complete || resp.Page.NextCursor == "" {
			return merged, true, nil
		}
		cursor = resp.Page.NextCursor
	}
	return merged, false, nil
}
