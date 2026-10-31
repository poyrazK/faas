package alerts_test

// adr: 20261031144636 — a recovery notice a channel did not take is retried
// with backoff, repeating what the first attempt reported, until it goes
// through, the rule fires again (which supersedes it), or a day has passed.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type channelRetryFixture struct {
	store    *state.MemStore
	rule     state.AlertRule
	channels *recordingChannels
	ev       *alerts.Evaluator
	mu       sync.Mutex
	now      time.Time
	value    float64
}

func newChannelRetryFixture(t *testing.T) *channelRetryFixture {
	t.Helper()
	f := &channelRetryFixture{store: state.NewMemStore(), now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), value: 12}
	rule, ident := seedChannelRule(t, f.store)
	f.rule = rule
	f.channels = &recordingChannels{fail: map[string]error{}}
	f.ev = alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: f.store,
		PromQL: &selectivePromQL{fn: func(string) (float64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.value, nil
		}},
		Audit:      audit.New(f.store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}},
		Channels:   f.channels,
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.now
		},
		Log: discardLog(),
	})
	return f
}

// tick sets the observed value and clock, runs one evaluation and returns
// the notifications sent to the Slack channel.
func (f *channelRetryFixture) tick(t *testing.T, value float64, at time.Time) (alerts.Stats, []alertchannels.Message) {
	t.Helper()
	f.mu.Lock()
	f.value, f.now = value, at
	f.mu.Unlock()
	f.channels.mu.Lock()
	f.channels.sent = nil
	f.channels.mu.Unlock()
	stats, err := f.ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.channels.mu.Lock()
	defer f.channels.mu.Unlock()
	var slack []alertchannels.Message
	for _, s := range f.channels.sent {
		if s.target.Kind == "slack" {
			slack = append(slack, s.msg)
		}
	}
	return stats, slack
}

func (f *channelRetryFixture) slackDown(down bool) {
	f.channels.mu.Lock()
	defer f.channels.mu.Unlock()
	f.channels.fail["slack"] = nil
	if down {
		f.channels.fail["slack"] = errors.New("slack returned 503")
	}
}

func TestEvaluator_ChannelResolveIsRetriedUntilDelivered(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.now
	if _, sent := f.tick(t, 12, t0); len(sent) != 1 || sent[0].Event != alertchannels.EventFire {
		t.Fatalf("fire = %+v", sent)
	}
	f.slackDown(true)
	resolvedAt := t0.Add(time.Minute)
	if _, sent := f.tick(t, 1, resolvedAt); len(sent) != 1 || sent[0].Event != alertchannels.EventResolve {
		t.Fatalf("resolve = %+v", sent)
	}
	// Not due before its first backoff.
	if stats, sent := f.tick(t, 1, resolvedAt.Add(30*time.Second)); len(sent) != 0 || stats.ChannelResolvesRetried != 0 {
		t.Fatalf("retried before the backoff: %+v %+v", stats, sent)
	}
	// Due, still failing: one more attempt, then a longer wait.
	if _, sent := f.tick(t, 1, resolvedAt.Add(time.Minute)); len(sent) != 1 {
		t.Fatalf("first retry = %+v", sent)
	}
	if _, sent := f.tick(t, 1, resolvedAt.Add(2*time.Minute)); len(sent) != 0 {
		t.Fatalf("retried again before the doubled backoff: %+v", sent)
	}
	f.slackDown(false)
	stats, sent := f.tick(t, 1, resolvedAt.Add(4*time.Minute))
	if len(sent) != 1 || stats.ChannelResolvesRetried != 1 {
		t.Fatalf("retry after recovery = %+v %+v", stats, sent)
	}
	if msg := sent[0]; msg.Event != alertchannels.EventResolve || msg.Observed != 1 || !msg.OccurredAt.Equal(resolvedAt) || msg.DedupKey != f.rule.ID {
		t.Fatalf("retried notice = %+v; want the original resolve", msg)
	}
	if _, sent := f.tick(t, 1, resolvedAt.Add(time.Hour)); len(sent) != 0 {
		t.Fatalf("a delivered resolve was sent again: %+v", sent)
	}
}

func TestEvaluator_ChannelResolveRetryEndsWithANewFireOrADay(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.now
	f.tick(t, 12, t0)
	f.slackDown(true)
	f.tick(t, 1, t0.Add(time.Minute))
	// The rule fires again: the old recovery would resolve the new incident.
	if stats, _ := f.tick(t, 12, t0.Add(31*time.Minute)); stats.Fired != 1 {
		t.Fatalf("second fire = %+v; want a fire past the 30-minute cool-down", stats)
	}
	if _, err := f.store.SetAlertRuleState(context.Background(), f.rule.ID, state.AlertStateOk, t0.Add(32*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if due, err := f.store.DueAlertChannelResolveRetries(context.Background(), t0.Add(48*time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("retries after a new fire = %+v, %v; want none", due, err)
	}

	f2 := newChannelRetryFixture(t)
	f2.tick(t, 12, t0)
	f2.slackDown(true)
	f2.tick(t, 1, t0.Add(time.Minute))
	stats, sent := f2.tick(t, 1, t0.Add(25*time.Hour))
	if stats.ChannelResolvesAbandoned != 1 || len(sent) != 0 {
		t.Fatalf("after a day = %+v %+v; want the retry abandoned unsent", stats, sent)
	}
	if due, err := f2.store.DueAlertChannelResolveRetries(context.Background(), t0.Add(48*time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("retries after abandoning = %+v, %v; want none", due, err)
	}
}
