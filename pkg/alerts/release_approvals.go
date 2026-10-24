package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseApprovalBatch bounds how many approval events one tick delivers.
const releaseApprovalBatch = 20

// releaseApprovalErrorMax fits release_approval_events.last_error.
const releaseApprovalErrorMax = 512

// deliverReleaseApprovalEvents sends due release approval events
// (ADR-20261020085706) to their channels. An event counts as delivered when
// at least one channel took it; when every channel failed it is retried with
// backoff until api.ReleaseApprovalNotifyAttemptsMax attempts. It reports
// how many events were delivered.
func (e *Evaluator) deliverReleaseApprovalEvents(ctx context.Context, now time.Time) int {
	events, ok := e.store.(state.ReleaseApprovalEventStore)
	if !ok || e.channels == nil {
		return 0
	}
	due, err := events.DueReleaseApprovalEvents(ctx, now, releaseApprovalBatch)
	if err != nil {
		e.log.Warn("alerts: list due release approval events", "err", err)
		return 0
	}
	delivered := 0
	for _, ev := range due {
		if ctx.Err() != nil {
			return delivered
		}
		attempt := e.deliverReleaseApprovalEvent(ctx, ev, now)
		if attempt.State == state.ReleaseApprovalEventDelivered {
			delivered++
		}
		if err := events.FinishReleaseApprovalEvent(ctx, ev.ID, attempt); err != nil && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("alerts: record release approval delivery", "event", ev.ID, "err", err)
		}
	}
	return delivered
}

func (e *Evaluator) deliverReleaseApprovalEvent(ctx context.Context, ev state.ReleaseApprovalEvent, now time.Time) state.ReleaseApprovalEventAttempt {
	var notice alertchannels.ReleaseNotice
	if err := json.Unmarshal(ev.Payload, &notice); err != nil || notice.Event == "" {
		return state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventFailed, LastError: "the event's message could not be read", NextAttemptAt: now, At: now}
	}
	chans := e.releaseApprovalChannels(ctx, ev)
	if len(chans) == 0 {
		return state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventFailed, LastError: "none of the policy's channels exist any more", NextAttemptAt: now, At: now}
	}
	msg := alertchannels.ReleaseMessage(notice, ev.CreatedAt)
	ids := e.unsealIdentities()
	sctx, cancel := context.WithTimeout(ctx, channelSendBudget)
	defer cancel()
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		delivered int
		lastErr   string
	)
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
			} else {
				lastErr = ch.Name + ": " + errMsg
			}
		}(ch)
	}
	wg.Wait()
	if delivered > 0 {
		return state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventDelivered, LastError: safetext.Truncate(lastErr, releaseApprovalErrorMax), NextAttemptAt: now, At: now}
	}
	attempt := state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventPending, LastError: safetext.Truncate(lastErr, releaseApprovalErrorMax), At: now,
		NextAttemptAt: now.Add(api.ReleaseApprovalNotifyRetryBase << ev.Attempts)}
	if ev.Attempts+1 >= api.ReleaseApprovalNotifyAttemptsMax {
		attempt.State = state.ReleaseApprovalEventFailed
		attempt.LastError = safetext.Truncate(fmt.Sprintf("gave up after %d attempts; %s", ev.Attempts+1, lastErr), releaseApprovalErrorMax)
	}
	return attempt
}

// releaseApprovalChannels loads the event's channels that still exist and can
// receive approvals.
func (e *Evaluator) releaseApprovalChannels(ctx context.Context, ev state.ReleaseApprovalEvent) []state.NotificationChannel {
	store, ok := e.store.(state.NotificationChannelStore)
	if !ok {
		return nil
	}
	var out []state.NotificationChannel
	for _, id := range ev.ChannelIDs {
		ch, err := store.GetNotificationChannel(ctx, ev.AccountID, id)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("alerts: read release approval channel", "channel", id, "err", err)
			}
			continue
		}
		if ch.Kind != alertchannels.KindPagerDuty {
			out = append(out, ch)
		}
	}
	return out
}
