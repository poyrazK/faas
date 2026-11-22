// Package savedinsightnotify runs saved insights on their schedule in
// meterd and sends their digests and threshold alerts to the account's
// notification channels (ADR-20261122190000).
package savedinsightnotify

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/state"
)

// Interval is how often the runner looks for due saved insights.
const Interval = time.Minute

// notReleasedRetry is how long a saved insight of an account without saved
// insights waits before it is looked at again.
const notReleasedRetry = time.Hour

// ChannelSender delivers a message to notification channels; the alert
// evaluator implements it with the alerting seal domain's identities.
type ChannelSender interface {
	SendToChannels(ctx context.Context, chans []state.NotificationChannel, msg alertchannels.Message, now time.Time) (attempted, delivered int)
}

// Store is what the runner reads and writes.
type Store interface {
	state.SavedInsightStore
	state.AnalyticsStore
}

// Runner claims due saved insights, runs them and notifies.
type Runner struct {
	Store    Store
	Channels ChannelSender
	// Released reports whether the saved insight's account still has saved
	// insights and product analytics; nil releases every account.
	Released func(ctx context.Context, si state.SavedInsight) bool
	// DashboardBaseURL, when set, links messages to the console.
	DashboardBaseURL string
	Log              *slog.Logger
	Now              func() time.Time
	// Outcome counts each run by outcome (ok, failed, not_released).
	Outcome func(outcome string)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) count(outcome string) {
	if r.Outcome != nil {
		r.Outcome(outcome)
	}
}

// Run loops until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.log().Warn("saved insights: tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce runs one batch of due saved insights and returns how many ran.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	now := r.now().UTC()
	due, err := r.Store.ClaimDueSavedInsights(ctx, now, now.Add(api.SavedInsightLease), api.SavedInsightRunBatch)
	if err != nil {
		return 0, fmt.Errorf("claim saved insights: %w", err)
	}
	for _, si := range due {
		settle := r.runOne(ctx, si, now)
		if err := r.Store.SettleSavedInsight(ctx, settle); err != nil {
			r.log().Warn("saved insights: settle", "insight", si.ID, "err", err)
		}
	}
	return len(due), nil
}

// runOne runs one saved insight and returns its settlement.
func (r *Runner) runOne(ctx context.Context, si state.SavedInsight, now time.Time) state.SavedInsightSettle {
	out := state.SavedInsightSettle{ID: si.ID, ClaimedUpdatedAt: si.UpdatedAt, Now: now, LastValue: si.LastValue,
		AlertState: si.AlertState, NextDigestAt: si.NextDigestAt}
	hasAlert := si.AlertComparison != ""
	if r.Released != nil && !r.Released(ctx, si) {
		r.count("not_released")
		next := now.Add(notReleasedRetry)
		out.LastError, out.NextCheckAt = "saved insights are not enabled for this account; nothing was sent", &next
		if out.NextDigestAt != nil && out.NextDigestAt.Before(next) {
			out.NextDigestAt, _ = insights.Schedule(si.Digest, false, next)
		}
		return out
	}
	digestDue := si.Digest != "" && (si.NextDigestAt == nil || !si.NextDigestAt.After(now))
	if digestDue || out.NextDigestAt == nil {
		out.NextDigestAt, _ = insights.Schedule(si.Digest, false, now)
	}
	_, out.NextCheckAt = insights.Schedule("", hasAlert, now)
	if out.NextDigestAt != nil && (out.NextCheckAt == nil || out.NextDigestAt.Before(*out.NextCheckAt)) {
		t := *out.NextDigestAt
		out.NextCheckAt = &t
	}
	runCtx, cancel := context.WithTimeout(ctx, api.AnalyticsQueryTimeout)
	res, err := insights.RunSaved(runCtx, r.Store, si, api.Plan(si.Plan).AnalyticsRetentionDays(), now)
	cancel()
	if err != nil {
		r.count("failed")
		out.LastError = err.Error()
		if hasAlert {
			out.AlertState = state.SavedInsightUnknown
		}
		return out
	}
	r.count("ok")
	value := res.Value
	out.LastValue, out.LastError = &value, ""
	chans := r.channels(ctx, si)
	if hasAlert {
		firing := insights.Compare(value, si.AlertComparison, si.AlertThreshold)
		switch {
		case firing && si.AlertState != state.SavedInsightFiring:
			r.send(ctx, chans, r.message(si, alertchannels.EventFire, value, now), now)
		case !firing && si.AlertState == state.SavedInsightFiring:
			r.send(ctx, chans, r.message(si, alertchannels.EventResolve, value, now), now)
		}
		out.AlertState = state.SavedInsightOK
		if firing {
			out.AlertState = state.SavedInsightFiring
		}
	}
	if digestDue {
		// A digest is a report, not an incident: PagerDuty gets none.
		var reports []state.NotificationChannel
		for _, ch := range chans {
			if ch.Kind != "pagerduty" {
				reports = append(reports, ch)
			}
		}
		r.send(ctx, reports, r.message(si, alertchannels.EventDigest, value, now), now)
	}
	return out
}

func (r *Runner) channels(ctx context.Context, si state.SavedInsight) []state.NotificationChannel {
	if len(si.ChannelIDs) == 0 {
		return nil
	}
	chans, err := r.Store.SavedInsightChannels(ctx, si.AccountID, si.ChannelIDs)
	if err != nil {
		r.log().Warn("saved insights: list channels", "insight", si.ID, "err", err)
		return nil
	}
	return chans
}

func (r *Runner) send(ctx context.Context, chans []state.NotificationChannel, msg alertchannels.Message, now time.Time) {
	if r.Channels == nil || len(chans) == 0 {
		return
	}
	r.Channels.SendToChannels(ctx, chans, msg, now)
}

// FormatValue renders a saved insight's value: a funnel's conversion and
// a retention rate as a percentage, an events total as a number.
func FormatValue(kind string, v float64) string {
	if kind == api.SavedInsightKindFunnel || kind == api.SavedInsightKindRetention {
		return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

var valueLabels = map[string]string{
	api.SavedInsightKindEvents:    "total",
	api.SavedInsightKindFunnel:    "overall conversion",
	api.SavedInsightKindRetention: "first-period retention",
}

func (r *Runner) message(si state.SavedInsight, event string, value float64, now time.Time) alertchannels.Message {
	label := valueLabels[si.Kind]
	summary := fmt.Sprintf("The %s over the last %d days in %s: %s.", label, si.RangeDays, si.EnvironmentSlug, FormatValue(si.Kind, value))
	switch event {
	case alertchannels.EventFire:
		summary = fmt.Sprintf("The %s is %s, %s %s. %s", label, FormatValue(si.Kind, value), comparisonWords[si.AlertComparison],
			FormatValue(si.Kind, si.AlertThreshold), summary)
	case alertchannels.EventResolve:
		summary = fmt.Sprintf("The %s is back to %s. %s", label, FormatValue(si.Kind, value), summary)
	}
	msg := alertchannels.Message{
		Event: event, RuleName: fmt.Sprintf("saved insight %q", si.Name), Metric: "saved_insight", Observed: value,
		Comparison: si.AlertComparison, Threshold: si.AlertThreshold, Window: strconv.Itoa(si.RangeDays) + "d",
		OccurredAt: now, DedupKey: "saved-insight-" + si.ID, Summary: summary, Severity: "warning",
		IdempotencyKey: "saved-insight-" + si.ID + "-" + event + "-" + now.UTC().Format(time.RFC3339),
		Footer:         "You receive this because a Gregale saved insight sends to this channel. Change it with `gregale insights saved`.",
	}
	if r.DashboardBaseURL != "" {
		msg.DashboardURL = r.DashboardBaseURL + "/dashboard/projects/" + si.ProjectID + "/insights/" + si.Key
	}
	return msg
}

var comparisonWords = map[string]string{"gt": "above", "gte": "at or above", "lt": "below", "lte": "at or below"}
