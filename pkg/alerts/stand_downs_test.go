package alerts_test

// adr: 20261104142830 — every alert episode that ends gets a recovery
// notice on the channels its fire reached: a recovery after degraded or
// unknown reads, a rule disabled or no longer covered while firing, and a
// rule deleted while firing. A rule that never fired owes nothing.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f *channelRetryFixture) episodeOpen(t *testing.T) bool {
	t.Helper()
	open, err := f.store.AlertRuleEpisodeOpen(context.Background(), f.rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func (f *channelRetryFixture) fire(t *testing.T) time.Time {
	t.Helper()
	t0 := f.now
	if _, sent := f.tick(t, 12, t0); len(sent) != 1 || sent[0].Event != alertchannels.EventFire {
		t.Fatalf("fire = %+v", sent)
	}
	return t0
}

func wantStandDown(t *testing.T, sent []alertchannels.Message, ruleID, reason string) {
	t.Helper()
	if len(sent) != 1 {
		t.Fatalf("notices = %+v; want one recovery notice", sent)
	}
	msg := sent[0]
	if msg.Event != alertchannels.EventResolve || msg.DedupKey != ruleID || msg.Summary == "" {
		t.Fatalf("notice = %+v; want a resolve for rule %s explaining it was %s", msg, ruleID, reason)
	}
}

func TestEvaluator_RecoveryAfterADegradedReadIsAnnounced(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.fire(t)
	f.readErr = errors.New("prometheus unavailable")
	if stats, sent := f.tick(t, 12, t0.Add(time.Minute)); stats.SkippedDegraded != 1 || len(sent) != 0 {
		t.Fatalf("degraded tick = %+v %+v", stats, sent)
	}
	f.readErr = nil
	_, sent := f.tick(t, 1, t0.Add(2*time.Minute))
	if len(sent) != 1 || sent[0].Event != alertchannels.EventResolve || sent[0].Observed != 1 {
		t.Fatalf("recovery after a degraded read = %+v; want the resolve", sent)
	}
	if f.episodeOpen(t) {
		t.Fatal("the episode is still open after the recovery")
	}
}

func TestEvaluator_RuleThatNeverFiredOwesNoNotice(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.now
	f.readErr = errors.New("prometheus unavailable")
	f.tick(t, 1, t0)
	f.readErr = nil
	if _, sent := f.tick(t, 1, t0.Add(time.Minute)); len(sent) != 0 {
		t.Fatalf("degraded → ok without a fire sent %+v", sent)
	}
	f.readErr = errors.New("prometheus unavailable")
	f.tick(t, 1, t0.Add(2*time.Minute))
	disabled := false
	if _, err := f.store.UpdateAlertRule(context.Background(), f.rule.ID, state.UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	stats, sent := f.tick(t, 1, t0.Add(3*time.Minute))
	if len(sent) != 0 || stats.StoodDown != 0 {
		t.Fatalf("disabling a degraded rule that never fired = %+v %+v; want no notice", stats, sent)
	}
	if rule, err := f.store.AlertRuleByID(context.Background(), f.rule.ID); err != nil || rule.State != state.AlertStateOk {
		t.Fatalf("rule = %+v, %v; want it reset to ok", rule, err)
	}
}

func TestEvaluator_DisabledFiringRuleStandsDown(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.fire(t)
	disabled := false
	if _, err := f.store.UpdateAlertRule(context.Background(), f.rule.ID, state.UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	stats, sent := f.tick(t, 12, t0.Add(time.Minute))
	wantStandDown(t, sent, f.rule.ID, state.StandDownDisabled)
	if stats.StoodDown != 1 || stats.ChannelStandDownsDelivered != 2 || f.episodeOpen(t) {
		t.Fatalf("stats = %+v, episode open %v; want one rule stood down and its episode closed", stats, f.episodeOpen(t))
	}
	rule, err := f.store.AlertRuleByID(context.Background(), f.rule.ID)
	if err != nil || rule.State != state.AlertStateOk {
		t.Fatalf("rule = %+v, %v; want ok", rule, err)
	}
	if _, sent := f.tick(t, 12, t0.Add(2*time.Minute)); len(sent) != 0 {
		t.Fatalf("a stood-down rule notified again: %+v", sent)
	}
}

func TestEvaluator_NotReleasedFiringRuleStandsDown(t *testing.T) {
	f := newChannelRetryFixture(t)
	t0 := f.fire(t)
	f.ev.SetRuleRelease(func(context.Context, string) alerts.RuleRelease {
		return func(context.Context, state.AlertRule) (bool, bool) { return false, false }
	})
	stats, sent := f.tick(t, 12, t0.Add(time.Minute))
	wantStandDown(t, sent, f.rule.ID, state.StandDownNotReleased)
	if stats.SkippedNotReleased != 1 || stats.StoodDown != 1 || f.episodeOpen(t) {
		t.Fatalf("stats = %+v; want the rule skipped, stood down and its episode closed", stats)
	}
}

func TestEvaluator_DeletedFiringRuleStillResolvesItsChannels(t *testing.T) {
	f := newChannelRetryFixture(t)
	f.now = time.Now().UTC() // the delete queues its notices at the wall clock, as the trigger's now() does
	t0 := f.fire(t)
	f.slackDown(true)
	if err := f.store.DeleteAlertRule(context.Background(), f.rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, sent := f.tick(t, 12, t0.Add(time.Minute)); len(sent) != 1 {
		t.Fatalf("first attempt = %+v", sent)
	}
	f.slackDown(false)
	stats, sent := f.tick(t, 12, t0.Add(3*time.Minute))
	wantStandDown(t, sent, f.rule.ID, state.StandDownDeleted)
	if stats.ChannelStandDownsDelivered != 1 {
		t.Fatalf("stats = %+v; want the retried notice delivered", stats)
	}
	if _, sent := f.tick(t, 12, t0.Add(time.Hour)); len(sent) != 0 {
		t.Fatalf("a delivered notice was sent again: %+v", sent)
	}
}

func TestEvaluator_StandDownIsAbandonedAfterADay(t *testing.T) {
	f := newChannelRetryFixture(t)
	f.now = time.Now().UTC()
	t0 := f.fire(t)
	f.slackDown(true)
	if err := f.store.DeleteAlertRule(context.Background(), f.rule.ID); err != nil {
		t.Fatal(err)
	}
	f.tick(t, 12, t0.Add(time.Minute))
	stats, sent := f.tick(t, 12, t0.Add(26*time.Hour))
	if stats.ChannelStandDownsAbandoned != 1 || len(sent) != 0 {
		t.Fatalf("after a day = %+v %+v; want the notice abandoned unsent", stats, sent)
	}
}
