package outbound

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIBudgetAlert is one budget threshold a settlement reached for the first
// time in its period (ADR-991). Limit and Used are tokens for token budgets
// and millicents for cost budgets.
type AIBudgetAlert struct {
	ID               string
	IntegrationID    string
	Budget           string
	AppID            string
	PeriodStart      time.Time
	ThresholdPercent int
	Limit            int64
	Used             int64
	CrossedAt        time.Time
}

// AIBudgetAlertRecorder settles usage like AIUsageBackend and also reports
// the alerts the settlement recorded, for metrics.
type AIBudgetAlertRecorder interface {
	RecordAIUsageAlerts(context.Context, AIUsageRecord) ([]AIBudgetAlert, error)
}

var aiBudgetAlertPercents = []int{api.OutboundAIBudgetAlertWarnPercent, api.OutboundAIBudgetAlertFullPercent}

// aiThresholdUnits is the first usage at or above percent of limit,
// computed without overflowing for large limits.
func aiThresholdUnits(limit int64, percent int) int64 {
	p := int64(percent)
	return limit/100*p + (limit%100*p+99)/100
}

// crossedAIBudgetThresholds returns the alert percents whose usage lies in
// (before, after]: the thresholds this settlement reached.
func crossedAIBudgetThresholds(limit *int64, before, after int64) []int {
	if limit == nil || *limit <= 0 || after <= before {
		return nil
	}
	var crossed []int
	for _, percent := range aiBudgetAlertPercents {
		if units := aiThresholdUnits(*limit, percent); before < units && units <= after {
			crossed = append(crossed, percent)
		}
	}
	return crossed
}

// aiBudgetCrossing is one budget's change during a settlement.
type aiBudgetCrossing struct {
	budget      string
	appID       string
	periodStart time.Time
	limit       *int64
	before      int64
	after       int64
}

// aiBudgetAlertsFor lists the alerts a settlement's budget changes reached.
func aiBudgetAlertsFor(integrationID string, crossings []aiBudgetCrossing, now time.Time) []AIBudgetAlert {
	var alerts []AIBudgetAlert
	for _, crossing := range crossings {
		for _, percent := range crossedAIBudgetThresholds(crossing.limit, crossing.before, crossing.after) {
			alerts = append(alerts, AIBudgetAlert{
				IntegrationID: integrationID, Budget: crossing.budget, AppID: crossing.appID, PeriodStart: crossing.periodStart,
				ThresholdPercent: percent, Limit: *crossing.limit, Used: crossing.after, CrossedAt: now,
			})
		}
	}
	return alerts
}

func utcDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func utcMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
