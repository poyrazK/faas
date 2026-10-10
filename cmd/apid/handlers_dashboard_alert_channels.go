package main

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// ruleDestinations names where a rule delivers: "webhook" and/or its
// notification channel names (ADR-749), or "nowhere" for a rule whose
// last channel was deleted.
func (s *server) ruleDestinations(ctx context.Context, rule state.AlertRule) string {
	var parts []string
	if rule.WebhookURL != "" {
		parts = append(parts, "webhook")
	}
	if bindings, ok := s.store.(state.AlertRuleChannelStore); ok {
		if chans, err := bindings.ListAlertRuleChannels(ctx, rule.ID); err == nil {
			for _, c := range chans {
				parts = append(parts, c.Name+" ("+c.Kind+")")
			}
		}
	}
	if len(parts) == 0 {
		return "nowhere — add a channel or webhook"
	}
	return strings.Join(parts, ", ")
}

// dashboardAlertChannels lists the account's channels with their last
// delivery outcome. Read failures hide the table rather than the page.
func (s *server) dashboardAlertChannels(ctx context.Context, acct state.Account, now time.Time) []dashboard.AlertChannelItem {
	store, ok := s.store.(state.NotificationChannelStore)
	if !ok {
		return nil
	}
	chans, err := store.ListNotificationChannels(ctx, acct.ID)
	if err != nil {
		return nil
	}
	out := make([]dashboard.AlertChannelItem, 0, len(chans))
	for _, c := range chans {
		out = append(out, alertChannelItem(c, now))
	}
	return out
}

func alertChannelItem(c state.NotificationChannel, now time.Time) dashboard.AlertChannelItem {
	item := dashboard.AlertChannelItem{Name: c.Name, Kind: c.Kind, Target: c.TargetHint, LastResult: "never used", LastClass: "dim"}
	switch {
	case !c.LastErrorAt.IsZero() && !c.LastErrorAt.Before(c.LastDeliveredAt):
		item.LastResult = "failed " + dashboard.RelativeTime(c.LastErrorAt, now) + ": " + dashboard.FormatAlertError(c.LastError)
		item.LastClass = "bad"
	case !c.LastDeliveredAt.IsZero():
		item.LastResult, item.LastClass = "delivered "+dashboard.RelativeTime(c.LastDeliveredAt, now), "ok"
	}
	return item
}
