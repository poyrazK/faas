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
	Log      *slog.Logger
	Now      func() time.Time
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
// Each one is settled before its notifications go out, so an edit made
// during the run (which skips the settle) never sends twice. Items the
// lease no longer covers are released for the next pass.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	now := r.now().UTC()
	leaseUntil := now.Add(api.SavedInsightLease)
	due, err := r.Store.ClaimDueSavedInsights(ctx, now, leaseUntil, api.SavedInsightRunBatch)
	if err != nil {
		return 0, fmt.Errorf("claim saved insights: %w", err)
	}
	ran := 0
	for _, si := range due {
		if r.now().Add(api.SavedInsightItemBudget).After(leaseUntil) {
			// Not enough lease left: release it unchanged.
			if err := r.Store.ReleaseSavedInsight(ctx, si.ID); err != nil {
				r.log().Warn("saved insights: release", "insight", si.ID, "err", err)
			}
			continue
		}
		ran++
		at := r.now().UTC()
		settle, msgs := r.runOne(ctx, si, at)
		applied, err := r.Store.SettleSavedInsight(ctx, settle)
		if err != nil {
			r.log().Warn("saved insights: settle", "insight", si.ID, "err", err)
			continue
		}
		if !applied {
			// Edited during the run: the next run decides with the new definition.
			continue
		}
		for _, m := range msgs {
			r.send(ctx, m.chans, m.msg, at)
		}
	}
	return ran, nil
}

type outgoing struct {
	chans []state.NotificationChannel
	msg   alertchannels.Message
}

// runOne runs one saved insight and returns its settlement and the
// notifications to send once it is settled.
func (r *Runner) runOne(ctx context.Context, si state.SavedInsight, now time.Time) (state.SavedInsightSettle, []outgoing) {
	out := state.SavedInsightSettle{ID: si.ID, ClaimedUpdatedAt: si.UpdatedAt, Now: now, LastValue: si.LastValue,
		AlertState: si.AlertState, NextDigestAt: si.NextDigestAt}
	hasAlert := si.AlertComparison != ""
	if r.Released != nil && !r.Released(ctx, si) {
		r.count("not_released")
		next := now.Add(api.SavedInsightNotReleasedRetry)
		out.LastError, out.NextCheckAt = "saved insights are not enabled for this account; nothing was sent", &next
		if out.NextDigestAt != nil && out.NextDigestAt.Before(next) {
			out.NextDigestAt, _ = insights.Schedule(si.Digest, false, next)
		}
		return out, nil
	}
	digestDue := si.Digest != "" && (si.NextDigestAt == nil || !si.NextDigestAt.After(now))
	_, out.NextCheckAt = insights.Schedule("", hasAlert, now)
	runCtx, cancel := context.WithTimeout(ctx, api.AnalyticsQueryTimeout)
	res, err := insights.RunSaved(runCtx, r.Store, si, api.Plan(si.Plan).AnalyticsRetentionDays(), now)
	cancel()
	if err != nil {
		// The alert keeps its state, so the next good run sends the right
		// transition; a due digest is retried at the alert interval.
		r.count("failed")
		out.LastError = err.Error()
		retry := now.Add(api.SavedInsightAlertInterval)
		out.NextCheckAt = &retry
		if !digestDue {
			// A due digest keeps its past time and is retried with the alert.
			out.NextCheckAt = earliest(out.NextCheckAt, out.NextDigestAt)
		}
		return out, nil
	}
	r.count("ok")
	if digestDue || out.NextDigestAt == nil {
		out.NextDigestAt, _ = insights.Schedule(si.Digest, false, now)
	}
	out.NextCheckAt = earliest(out.NextCheckAt, out.NextDigestAt)
	out.LastValue, out.LastError = res.Value, ""
	chans := r.channels(ctx, si)
	var msgs []outgoing
	// Without a value (no data in the range) the alert keeps its state.
	if hasAlert && res.Value != nil {
		firing := insights.Compare(*res.Value, si.AlertComparison, si.AlertThreshold)
		switch {
		case firing && si.AlertState != state.SavedInsightFiring:
			msgs = append(msgs, outgoing{chans, r.message(si, alertchannels.EventFire, res.Value, now)})
		case !firing && si.AlertState == state.SavedInsightFiring:
			msgs = append(msgs, outgoing{chans, r.message(si, alertchannels.EventResolve, res.Value, now)})
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
		msgs = append(msgs, outgoing{reports, r.message(si, alertchannels.EventDigest, res.Value, now)})
	}
	return out, msgs
}

// earliest returns the earlier of two optional times.
func earliest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.Before(*a)) {
		if b == nil {
			return nil
		}
		t := *b
		return &t
	}
	return a
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

func (r *Runner) message(si state.SavedInsight, event string, value *float64, now time.Time) alertchannels.Message {
	label := valueLabels[si.Kind]
	shown, observed := "no data", 0.0
	if value != nil {
		shown, observed = FormatValue(si.Kind, *value), *value
	}
	summary := fmt.Sprintf("The %s over the last %d days in %s: %s.", label, si.RangeDays, si.EnvironmentSlug, shown)
	switch event {
	case alertchannels.EventFire:
		summary = fmt.Sprintf("The %s is %s, %s %s. %s", label, shown, comparisonWords[si.AlertComparison],
			FormatValue(si.Kind, si.AlertThreshold), summary)
	case alertchannels.EventResolve:
		summary = fmt.Sprintf("The %s is back to %s. %s", label, shown, summary)
	}
	// No dashboard link: the console has no saved insight page yet.
	return alertchannels.Message{
		Event: event, RuleName: fmt.Sprintf("saved insight %q", si.Name), Metric: "saved_insight", Observed: observed,
		Comparison: si.AlertComparison, Threshold: si.AlertThreshold, Window: strconv.Itoa(si.RangeDays) + "d",
		OccurredAt: now, DedupKey: "saved-insight-" + si.ID, Summary: summary, Severity: "warning",
		IdempotencyKey: "saved-insight-" + si.ID + "-" + event + "-" + now.UTC().Format(time.RFC3339),
		Footer:         "You receive this because a Gregale saved insight sends to this channel. Change it with `gregale insight saved`.",
	}
}

var comparisonWords = map[string]string{"gt": "above", "gte": "at or above", "lt": "below", "lte": "at or below"}
