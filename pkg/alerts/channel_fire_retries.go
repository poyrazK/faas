package alerts

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/state"
)

// A fire a notification channel did not take is retried
// (ADR-20261109120000). The sender tries three times over about three
// seconds, and the next fire waits for the rule's cool-down (15 minutes by
// default, up to a day), so a short PagerDuty or Slack outage at the moment
// of a fire lost the page for that long, or for good when the breach ended
// sooner. A failed fire is now retried with backoff while its rule is still
// firing and until the rule's next fire is due, which would send it anyway.
//
// The retries are kept in memory: meterd evaluates alerts in one process, and
// a restart loses at most the retries until the rule's next fire. Recovery
// notices keep their durable queue (ADR-20261031144636), since nothing else
// would send them again.

// fireRetryBackoffMax bounds the wait between attempts.
const fireRetryBackoffMax = 5 * time.Minute

type fireRetry struct {
	ruleID, channelID string
	observed          float64
	firedAt           time.Time
	attempts          int
	next              time.Time
}

type fireRetries struct {
	mu      sync.Mutex
	pending map[string]fireRetry // rule id + "/" + channel id
}

func fireRetryKey(ruleID, channelID string) string { return ruleID + "/" + channelID }

// fireRetryBackoff is the wait before attempt n+1: 30 seconds, doubling, at
// most fireRetryBackoffMax.
func fireRetryBackoff(attempts int) time.Duration {
	return min(30*time.Second<<min(max(attempts-1, 0), 10), fireRetryBackoffMax)
}

// trackChannelFire records a fire a channel did not take for retry, or
// forgets one that went through.
func (e *Evaluator) trackChannelFire(rule state.AlertRule, ch state.NotificationChannel, observed float64, now time.Time, errMsg string) {
	e.fireRetries.mu.Lock()
	defer e.fireRetries.mu.Unlock()
	key := fireRetryKey(rule.ID, ch.ID)
	if errMsg == "" {
		delete(e.fireRetries.pending, key)
		return
	}
	if e.fireRetries.pending == nil {
		e.fireRetries.pending = map[string]fireRetry{}
	}
	e.fireRetries.pending[key] = fireRetry{ruleID: rule.ID, channelID: ch.ID, observed: observed, firedAt: now, attempts: 1, next: now.Add(fireRetryBackoff(1))}
}

// clearChannelFires drops a rule's pending fire retries: the rule recovered,
// stood down or fired again, and a late page would contradict that.
func (e *Evaluator) clearChannelFires(ruleID string) {
	e.fireRetries.mu.Lock()
	defer e.fireRetries.mu.Unlock()
	for key, r := range e.fireRetries.pending {
		if r.ruleID == ruleID {
			delete(e.fireRetries.pending, key)
		}
	}
}

// dueChannelFires removes and returns the retries due at now.
func (e *Evaluator) dueChannelFires(now time.Time) []fireRetry {
	e.fireRetries.mu.Lock()
	defer e.fireRetries.mu.Unlock()
	var due []fireRetry
	for key, r := range e.fireRetries.pending {
		if !now.Before(r.next) {
			due = append(due, r)
			delete(e.fireRetries.pending, key)
		}
	}
	return due
}

// retryChannelFires re-sends the due fires of rules that are still firing
// and whose next fire is not yet due. It returns how many went through.
func (e *Evaluator) retryChannelFires(ctx context.Context, now time.Time) (retried int) {
	bindings, bound := e.store.(state.AlertRuleChannelStore)
	if !bound || e.channels == nil {
		return 0
	}
	ids := e.unsealIdentities()
	for _, r := range e.dueChannelFires(now) {
		if ctx.Err() != nil {
			return retried
		}
		rule, err := e.store.AlertRuleByID(ctx, r.ruleID)
		if err != nil || !rule.Enabled || rule.State != state.AlertStateFiring {
			continue
		}
		cooldown := max(time.Duration(rule.CooldownMinutes)*time.Minute, time.Minute)
		if !now.Before(r.firedAt.Add(cooldown)) || !e.channelsReleased(ctx, rule) {
			continue // the next fire is due and will page on its own
		}
		ch, found := boundChannel(ctx, bindings, rule.ID, r.channelID)
		if !found {
			continue
		}
		errMsg := ""
		if target, ok := openTarget(ids, ch); !ok {
			errMsg = "could not open the channel destination; recreate the channel"
		} else {
			sctx, cancel := context.WithTimeout(ctx, channelSendBudget)
			if err := e.channels.Send(sctx, target, e.channelMessage(ctx, rule, alertchannels.EventFire, r.observed, r.firedAt)); err != nil {
				errMsg = err.Error()
			}
			cancel()
		}
		outcome := ChannelOutcomeDelivered
		if errMsg != "" {
			outcome = ChannelOutcomeFailed
		}
		e.recordChannel(ctx, ch, now, errMsg, outcome)
		if errMsg == "" {
			retried++
			continue
		}
		r.attempts++
		r.next = now.Add(fireRetryBackoff(r.attempts))
		e.fireRetries.mu.Lock()
		if e.fireRetries.pending == nil {
			e.fireRetries.pending = map[string]fireRetry{}
		}
		// A fire recorded meanwhile is newer; keep it.
		if _, newer := e.fireRetries.pending[fireRetryKey(r.ruleID, r.channelID)]; !newer {
			e.fireRetries.pending[fireRetryKey(r.ruleID, r.channelID)] = r
		}
		e.fireRetries.mu.Unlock()
	}
	return retried
}
