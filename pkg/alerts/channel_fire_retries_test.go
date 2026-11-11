package alerts_test

// adr: 20261109153000 — a fire a channel did not take is retried with
// backoff while the rule is still firing and before its next fire is due;
// a recovery or a stand-down drops it, so a late page never reopens an
// incident that has ended.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
)

func TestEvaluator_ChannelFireIsRetriedWhileFiring(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.now
	f.slackDown(true)
	if _, sent := f.tick(t, 12, t0); len(sent) != 1 || sent[0].Event != alertchannels.EventFire {
		t.Fatalf("fire = %+v", sent)
	}
	// Still breaching inside the cool-down: nothing is due yet.
	if stats, sent := f.tick(t, 12, t0.Add(10*time.Second)); len(sent) != 0 || stats.ChannelFiresRetried != 0 {
		t.Fatalf("retried before the backoff: %+v %+v", stats, sent)
	}
	// Due, still failing: one more attempt, then a longer wait.
	if _, sent := f.tick(t, 12, t0.Add(30*time.Second)); len(sent) != 1 {
		t.Fatalf("first retry = %+v", sent)
	}
	if _, sent := f.tick(t, 12, t0.Add(60*time.Second)); len(sent) != 0 {
		t.Fatalf("retried again before the doubled backoff: %+v", sent)
	}
	f.slackDown(false)
	stats, sent := f.tick(t, 12, t0.Add(2*time.Minute))
	if len(sent) != 1 || stats.ChannelFiresRetried != 1 {
		t.Fatalf("retry after recovery = %+v %+v", stats, sent)
	}
	if msg := sent[0]; msg.Event != alertchannels.EventFire || msg.Observed != 12 || !msg.OccurredAt.Equal(t0) || msg.DedupKey != f.rule.ID {
		t.Fatalf("retried page = %+v; want the original fire", msg)
	}
	if _, sent := f.tick(t, 12, t0.Add(10*time.Minute)); len(sent) != 0 {
		t.Fatalf("a delivered fire was sent again: %+v", sent)
	}
}

func TestEvaluator_ChannelFireRetryEndsWhenTheRuleRecovers(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.now
	f.slackDown(true)
	f.tick(t, 12, t0)
	// The rule recovers before the page went through.
	f.tick(t, 1, t0.Add(20*time.Second))
	f.slackDown(false)
	_, sent := f.tick(t, 1, t0.Add(5*time.Minute))
	for _, msg := range sent {
		if msg.Event == alertchannels.EventFire {
			t.Fatalf("a fire was retried after the rule recovered: %+v", sent)
		}
	}
}
