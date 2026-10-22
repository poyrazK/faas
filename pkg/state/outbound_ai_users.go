package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OutboundAIUserUsageItem is one end user's UTC-day AI usage on an
// integration, written by outboundd (ADR-994).
type OutboundAIUserUsageItem struct {
	EndUser        string
	Requests       int64
	ErrorRequests  int64
	InputTokens    int64
	OutputTokens   int64
	CostMillicents int64
}

// OutboundAIUserUsage is one UTC day of per-end-user usage, most expensive
// first. TrackedUsers counts the day's rows, including the overflow row.
type OutboundAIUserUsage struct {
	UsageDate           string
	UserDailyTokenLimit *int64
	UserDailyCostLimit  *int64
	TrackedUsers        int64
	Items               []OutboundAIUserUsageItem
}

// OutboundAIUserUsageRow is one end user's UTC day in an export (ADR-995).
type OutboundAIUserUsageRow struct {
	UsageDate string
	OutboundAIUserUsageItem
}

// OutboundAIUserStore reads the per-end-user rollups outboundd writes. An
// empty endUser lists the day's top users; otherwise only that user's row.
type OutboundAIUserStore interface {
	GetOutboundAIUserUsage(ctx context.Context, accountID, integrationID, usageDate, endUser string, limit int) (OutboundAIUserUsage, error)
	// ExportOutboundAIUserUsage visits every per-user row of the UTC days
	// from through to, ordered by day and end user (ADR-995). It returns
	// ErrNotFound or ErrInvalidArgument before visiting any row.
	ExportOutboundAIUserUsage(ctx context.Context, accountID, integrationID, from, to string, visit func(OutboundAIUserUsageRow) error) error
}

// outboundAIUserExportRange validates an export's inclusive day range; both
// days must be inside the per-user rollup retention.
func outboundAIUserExportRange(from, to string, now time.Time) (string, string, bool) {
	from, okFrom := outboundAIUserUsageQuery(from, "", now)
	to, okTo := outboundAIUserUsageQuery(to, "", now)
	return from, to, okFrom && okTo && from <= to
}

// outboundAIUserUsageQuery validates a per-user usage read and defaults the
// day to today. The day must be inside the per-user rollup retention.
func outboundAIUserUsageQuery(usageDate, endUser string, now time.Time) (string, bool) {
	utcNow := now.UTC()
	if usageDate == "" {
		usageDate = utcNow.Format("2006-01-02")
	}
	day, err := time.Parse("2006-01-02", usageDate)
	if err != nil || (endUser != "" && endUser != api.OutboundAIUserOverflow && !api.ValidOutboundAIUserID(endUser)) {
		return "", false
	}
	today := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	oldest := today.AddDate(0, 0, -api.OutboundAIUserUsageRetentionDays)
	return usageDate, !day.Before(oldest) && !day.After(today)
}
