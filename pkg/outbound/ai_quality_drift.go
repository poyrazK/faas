package outbound

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Drift alerts (ADR 20261021171712): a model's quality today against its
// own previous days, for integrations that turned quality alerts on.

// AIQualityTotals are one model's quality counts over a span of days.
// Answered counts calls that answered with known usage, which are the
// calls flags can mark.
type AIQualityTotals struct {
	Answered, Truncated, Refused, Empty, InvalidJSON, ToolError int64
	GoodFeedback, BadFeedback                                   int64
	Evaluated, ScoreSum                                         int64
}

// AIQualityWindow is one integration's model: its UTC day so far and its
// baseline days.
type AIQualityWindow struct {
	IntegrationID   string
	AccountID       string
	IntegrationName string
	Model           string
	UsageDate       time.Time
	Today, Baseline AIQualityTotals
}

// AIQualityAlert is one drift found: rates in basis points, scores in
// hundredths, and the counts they were taken over.
type AIQualityAlert struct {
	ID              string
	IntegrationID   string
	AccountID       string
	IntegrationName string
	UsageDate       time.Time
	Model           string
	Metric          string
	CurrentValue    int64
	BaselineValue   int64
	CurrentCount    int64
	BaselineCount   int64
	DetectedAt      time.Time
}

// AIQualityDriftStore reads windows and records alerts once per
// integration, model, metric and day.
type AIQualityDriftStore interface {
	AIQualityWindows(ctx context.Context) ([]AIQualityWindow, error)
	// RecordAIQualityAlert stores a first-time alert and notifies the
	// integration's bound apps; it reports whether the alert was new.
	RecordAIQualityAlert(ctx context.Context, alert AIQualityAlert) (bool, error)
}

// basisPoints is part as basis points of whole.
func basisPoints(part, whole int64) int64 {
	if whole <= 0 {
		return 0
	}
	return part * 10_000 / whole
}

// rateDrifted reports a rate up by at least OutboundAIQualityDriftPoints
// percentage points and at least doubled.
func rateDrifted(current, baseline int64) bool {
	return current >= baseline+api.OutboundAIQualityDriftPoints*100 && current >= 2*baseline
}

// detectAIQualityDrift lists the drifts in one window.
func detectAIQualityDrift(window AIQualityWindow, now time.Time) []AIQualityAlert {
	today, base := window.Today, window.Baseline
	alert := func(metric string, current, baseline, currentCount, baselineCount int64) AIQualityAlert {
		return AIQualityAlert{IntegrationID: window.IntegrationID, AccountID: window.AccountID, IntegrationName: window.IntegrationName,
			UsageDate: window.UsageDate, Model: window.Model, Metric: metric, CurrentValue: current, BaselineValue: baseline,
			CurrentCount: currentCount, BaselineCount: baselineCount, DetectedAt: now}
	}
	var alerts []AIQualityAlert
	if today.Answered >= api.OutboundAIQualityDriftMinCalls && base.Answered >= api.OutboundAIQualityDriftMinBaselineCalls {
		for _, flag := range []struct {
			metric          string
			current, before int64
		}{
			{AIQualityTruncated, today.Truncated, base.Truncated},
			{AIQualityRefused, today.Refused, base.Refused},
			{AIQualityEmpty, today.Empty, base.Empty},
			{AIQualityInvalidJSON, today.InvalidJSON, base.InvalidJSON},
			{AIQualityToolError, today.ToolError, base.ToolError},
		} {
			current, baseline := basisPoints(flag.current, today.Answered), basisPoints(flag.before, base.Answered)
			if rateDrifted(current, baseline) {
				alerts = append(alerts, alert(flag.metric, current, baseline, today.Answered, base.Answered))
			}
		}
	}
	rated, ratedBefore := today.GoodFeedback+today.BadFeedback, base.GoodFeedback+base.BadFeedback
	if rated >= api.OutboundAIQualityDriftMinEvaluations && ratedBefore >= api.OutboundAIQualityDriftMinEvaluations {
		current, baseline := basisPoints(today.BadFeedback, rated), basisPoints(base.BadFeedback, ratedBefore)
		if rateDrifted(current, baseline) {
			alerts = append(alerts, alert(api.OutboundAIQualityMetricBadFeedback, current, baseline, rated, ratedBefore))
		}
	}
	if today.Evaluated >= api.OutboundAIQualityDriftMinEvaluations && base.Evaluated >= api.OutboundAIQualityDriftMinEvaluations {
		current, baseline := today.ScoreSum*100/today.Evaluated, base.ScoreSum*100/base.Evaluated
		if baseline-current >= api.OutboundAIQualityDriftScoreDropTenths*10 {
			alerts = append(alerts, alert(api.OutboundAIQualityMetricEvaluationScore, current, baseline, today.Evaluated, base.Evaluated))
		}
	}
	return alerts
}

// AIQualityDriftWorker checks every quality-alerting integration on an
// interval.
type AIQualityDriftWorker struct {
	Store    AIQualityDriftStore
	Metrics  *Metrics
	Interval time.Duration
	Now      func() time.Time
	Log      *slog.Logger
}

// Run checks until ctx ends.
func (w *AIQualityDriftWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = api.OutboundAIQualityDriftCheckMinutes * time.Minute
	}
	for {
		if _, err := w.CheckOnce(ctx); err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("outboundd: AI quality drift", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// CheckOnce reads every window and records its drifts, returning the new
// alerts.
func (w *AIQualityDriftWorker) CheckOnce(ctx context.Context) ([]AIQualityAlert, error) {
	windows, err := w.Store.AIQualityWindows(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	var recorded []AIQualityAlert
	for _, window := range windows {
		for _, alert := range detectAIQualityDrift(window, now.UTC()) {
			created, err := w.Store.RecordAIQualityAlert(ctx, alert)
			if err != nil {
				return recorded, err
			}
			if created {
				w.Metrics.ObserveAIQualityAlert(alert.Metric)
				recorded = append(recorded, alert)
			}
		}
	}
	return recorded, nil
}
