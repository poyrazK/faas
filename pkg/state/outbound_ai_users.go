package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OutboundAIUserUsageItem is one end user's UTC-day AI usage on an
// integration, written by outboundd (ADR-992).
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

// OutboundAIUserStore reads the per-end-user rollups outboundd writes. An
// empty endUser lists the day's top users; otherwise only that user's row.
type OutboundAIUserStore interface {
	GetOutboundAIUserUsage(ctx context.Context, accountID, integrationID, usageDate, endUser string, limit int) (OutboundAIUserUsage, error)
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
