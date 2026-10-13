package alerts

import (
	"context"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChannelSender delivers one notification; *alertchannels.Sender
// implements it.
type ChannelSender interface {
	Send(ctx context.Context, t alertchannels.Target, msg alertchannels.Message) error
}

// channelSendBudget bounds how long one rule's channel fan-out can hold the
// single-goroutine evaluator, retries included.
const channelSendBudget = 30 * time.Second

// Channel delivery outcomes for meterd_alert_channel_deliveries_total.
const (
	ChannelOutcomeDelivered = "delivered"
	ChannelOutcomeFailed    = "failed"
	ChannelOutcomeUnsealErr = "unseal_failed"
)

// SetChannels wires ADR-749 channel delivery after construction, for
// callers (cmd/meterd) that build the mail transport separately. Call it
// before the first RunOnce; outcome must be safe for concurrent use.
func (e *Evaluator) SetChannels(sender ChannelSender, outcome func(kind, outcome string)) {
	if e == nil {
		return
	}
	e.channels, e.channelOutcome = sender, outcome
}

// notifyChannels sends event to every channel bound to rule (ADR-749),
// concurrently and best effort: one failing channel never blocks another,
// the webhook, or the rule's action. Each outcome is recorded on its
// channel. It returns how many channels were attempted and delivered.
func (e *Evaluator) notifyChannels(ctx context.Context, rule state.AlertRule, event string, observed float64, now time.Time) (attempted, delivered int) {
	if e.channels == nil {
		return 0, 0
	}
	bindings, ok := e.store.(state.AlertRuleChannelStore)
	if !ok {
		return 0, 0
	}
	chans, err := bindings.ListAlertRuleChannels(ctx, rule.ID)
	if err != nil {
		e.log.Warn("alerts: list rule channels", "rule", rule.ID, "err", err)
		return 0, 0
	}
	if len(chans) == 0 {
		return 0, 0
	}
	msg := e.channelMessage(ctx, rule, event, observed, now)
	ids := e.unsealIdentities()
	sctx, cancel := context.WithTimeout(ctx, channelSendBudget)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ch := range chans {
		wg.Add(1)
		go func(ch state.NotificationChannel) {
			defer wg.Done()
			outcome, errMsg := ChannelOutcomeDelivered, ""
			target, ok := openTarget(ids, ch)
			if !ok {
				outcome, errMsg = ChannelOutcomeUnsealErr, "could not open the channel destination; recreate the channel"
			} else if err := e.channels.Send(sctx, target, msg); err != nil {
				outcome, errMsg = ChannelOutcomeFailed, err.Error()
			}
			e.recordChannel(ctx, ch, now, errMsg, outcome)
			mu.Lock()
			defer mu.Unlock()
			if outcome == ChannelOutcomeDelivered {
				delivered++
			}
		}(ch)
	}
	wg.Wait()
	return len(chans), delivered
}

func (e *Evaluator) recordChannel(ctx context.Context, ch state.NotificationChannel, now time.Time, errMsg, outcome string) {
	if store, ok := e.store.(state.NotificationChannelStore); ok {
		if err := store.RecordNotificationChannelDelivery(ctx, ch.ID, now, errMsg); err != nil {
			e.log.Warn("alerts: record channel delivery", "channel", ch.ID, "err", err)
		}
	}
	if e.channelOutcome != nil {
		e.channelOutcome(ch.Kind, outcome)
	}
	if errMsg != "" {
		e.log.Warn("alerts: channel delivery failed", "channel", ch.ID, "kind", ch.Kind, "err", errMsg)
	}
}

func (e *Evaluator) channelMessage(ctx context.Context, rule state.AlertRule, event string, observed float64, now time.Time) alertchannels.Message {
	msg := alertchannels.Message{
		Event: event, RuleName: rule.Name, Metric: string(rule.Metric), Comparison: string(rule.Comparison),
		Threshold: rule.Threshold, Observed: observed, Window: string(rule.WindowSpec), OccurredAt: now, DedupKey: rule.ID,
	}
	if rule.AppID == "" {
		return msg
	}
	if apps, ok := e.store.(interface {
		AppByID(context.Context, string) (state.App, error)
	}); ok {
		if app, err := apps.AppByID(ctx, rule.AppID); err == nil {
			msg.AppSlug = app.Slug
			if e.dashboardBaseURL != "" {
				msg.DashboardURL = e.dashboardBaseURL + "/dashboard/apps/" + app.Slug
			}
		}
	}
	return msg
}

// unsealIdentities returns the host identities that open sealed secrets,
// preferring the rotation-aware accessor.
func (e *Evaluator) unsealIdentities() []*age.X25519Identity {
	if e.identities != nil {
		if ids := e.identities(); len(ids) > 0 {
			return ids
		}
	}
	if e.identity != nil {
		if id := e.identity(); id != nil {
			return []*age.X25519Identity{id}
		}
	}
	return nil
}

func openTarget(ids []*age.X25519Identity, ch state.NotificationChannel) (alertchannels.Target, bool) {
	t := alertchannels.Target{Kind: ch.Kind, Region: ch.PagerDutyRegion, Email: ch.Email}
	if ch.Kind == alertchannels.KindEmail {
		return t, true
	}
	if len(ids) == 0 {
		return t, false
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(ids, ch.TargetSealed)
	if err != nil || ns != alertchannels.SealNamespace {
		return t, false
	}
	if ch.Kind == alertchannels.KindSlack {
		t.SlackURL = string(plaintext)
	} else {
		t.RoutingKey = string(plaintext)
	}
	return t, true
}
