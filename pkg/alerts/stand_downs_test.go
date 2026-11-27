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

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/webhookout"
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

func TestEvaluator_ObserveOnlyStandsDownLiveAlert(t *testing.T) {
	f := newChannelRetryFixture(t)
	f.fire(t)
	rule, err := f.store.UpdateAlertRule(t.Context(), f.rule.ID, state.UpdateAlertRuleParams{
		Comparison: new(state.AlertAboveBaseline), ObserveOnly: new(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, sent := f.tick(t, 12, rule.UpdatedAt.Add(time.Minute))
	wantStandDown(t, sent, rule.ID, state.StandDownDisabled)
	if stats.Fired != 0 || stats.ChannelStandDownsDelivered != 2 || f.episodeOpen(t) {
		t.Fatalf("observe-only tick = %+v; episode open=%v", stats, f.episodeOpen(t))
	}
	if _, sent := f.tick(t, 12, rule.UpdatedAt.Add(2*time.Minute)); len(sent) != 0 {
		t.Fatalf("observe-only check sent %+v after the stand-down", sent)
	}
}

type pagerDutyHandoffCase struct {
	name, replacementKey string
	telemetryGap         bool
	rebindAfterClaim     bool
	rotateOriginal       bool
}

func TestEvaluator_ObserveOnlyRepromotionResolvesPagerDutyBeforeRefiring(t *testing.T) {
	const originalKey = "0123456789abcdef0123456789abcdef"
	for _, tc := range []pagerDutyHandoffCase{
		{name: "same channel"},
		{name: "alias with a telemetry gap", replacementKey: originalKey, telemetryGap: true},
		{name: "independent destination", replacementKey: "fedcba9876543210fedcba9876543210"},
		{name: "alias rebound after the claim", replacementKey: originalKey, rebindAfterClaim: true},
		{name: "original destination rotated after queueing", replacementKey: originalKey, rotateOriginal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPagerDutyRepromotion(t, originalKey, tc)
		})
	}
}

type pagerDutyHandoffProm struct {
	*anomalyProm
	unavailable bool
}

func (p *pagerDutyHandoffProm) QueryVectorStrictAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error) {
	if p.unavailable {
		return nil, errors.New("telemetry temporarily unavailable")
	}
	return p.anomalyProm.QueryVectorStrictAt(ctx, query, at)
}

func checkPagerDutyRepromotion(t *testing.T, originalKey string, tc pagerDutyHandoffCase) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricRequestCount, state.AlertAboveBaseline, 3)
	sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte(originalKey), 256)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{
		AccountID: rule.AccountID, Kind: alertchannels.KindPagerDuty, Name: "on-call", TargetSealed: sealed, PagerDutyRegion: "us",
	}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{channel.ID}); err != nil {
		t.Fatal(err)
	}
	edit := func(observing bool, scope *string) state.AlertRule {
		t.Helper()
		updated, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: &observing, EnvironmentScope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return updated
	}
	edit(true, new("production"))
	live := edit(false, nil)
	now := live.UpdatedAt.Add(time.Minute)
	channels := &recordingChannels{fail: map[string]error{}}
	prom := &pagerDutyHandoffProm{anomalyProm: &anomalyProm{currentSamples: 400, pastSamples: 100, trafficDays: 7}}
	var afterClaim func()
	newEvaluator := func() *alerts.Evaluator {
		return alerts.NewEvaluator(alerts.EvaluatorOptions{
			Store: statetest.NewMem(fireClaimHook{MemStore: store, after: func() {
				if afterClaim != nil {
					afterClaim()
				}
			}}), PromQL: prom,
			Identity: func() *age.X25519Identity { return ident }, Now: func() time.Time { return now },
			Channels: channels, Log: discardLog(), Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200}},
		})
	}
	ev := newEvaluator()
	if stats, err := ev.RunOnce(ctx); err != nil || stats.Fired != 1 || len(channels.sent) != 1 {
		t.Fatalf("initial PagerDuty fire: stats=%+v sent=%+v err=%v", stats, channels.sent, err)
	}
	edit(true, new("staging"))
	if tc.rotateOriginal {
		rotated, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte("fedcba9876543210fedcba9876543210"), 256)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateNotificationChannel(ctx, rule.AccountID, channel.ID, state.UpdateNotificationChannelParams{
			TargetSealed: rotated, PagerDutyRegion: new("eu"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if tc.replacementKey != "" {
		sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte(tc.replacementKey), 256)
		if err != nil {
			t.Fatal(err)
		}
		alias, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{
			AccountID: rule.AccountID, Kind: alertchannels.KindPagerDuty, Name: "replacement", TargetSealed: sealed, PagerDutyRegion: "us",
		}, api.MaxNotificationChannelsPerAccount)
		if err != nil {
			t.Fatal(err)
		}
		bind := func() {
			if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{alias.ID}); err != nil {
				t.Error(err)
			}
		}
		if tc.rebindAfterClaim {
			afterClaim = bind
		} else {
			bind()
		}
	}
	live = edit(false, nil)
	// Keep the evaluator clock after the edit and the prior evaluation.
	if live.UpdatedAt.After(now) {
		now = live.UpdatedAt
	}
	now = now.Add(time.Minute)
	channels.fail[alertchannels.KindPagerDuty] = errors.New("PagerDuty unavailable")
	stats, err := ev.RunOnce(ctx)
	if tc.rebindAfterClaim {
		if err != nil || stats.Fired != 1 || len(channels.sent) != 2 || channels.sent[1].msg.Event != alertchannels.EventResolve {
			t.Fatalf("a binding added after the claim must not overtake the old resolve: stats=%+v attempts=%d err=%v", stats, len(channels.sent), err)
		}
		return
	}
	if tc.replacementKey != "" && tc.replacementKey != originalKey {
		if err != nil || len(channels.sent) != 3 || channels.sent[2].msg.Event != alertchannels.EventFire || channels.sent[2].target.RoutingKey != tc.replacementKey {
			t.Fatalf("the distinct destination should still attempt its fire: stats=%+v attempts=%d err=%v", stats, len(channels.sent), err)
		}
		return
	}
	if err != nil || stats.Fired != 1 || len(channels.sent) != 2 || channels.sent[1].msg.Event != alertchannels.EventResolve {
		t.Fatalf("the unavailable destination should only attempt the old resolve: stats=%+v attempts=%d err=%v", stats, len(channels.sent), err)
	}
	channels.fail[alertchannels.KindPagerDuty] = nil
	now = now.Add(time.Minute)
	if tc.telemetryGap {
		prom.unavailable = true
		stats, err := newEvaluator().RunOnce(ctx)
		if err != nil || stats.ChannelStandDownsDelivered != 1 || len(channels.sent) != 3 {
			t.Fatalf("the old resolve should go through during the telemetry gap: stats=%+v attempts=%d err=%v", stats, len(channels.sent), err)
		}
		prom.unavailable = false
		now = now.Add(time.Minute)
	}
	stats, err = newEvaluator().RunOnce(ctx)
	if err != nil || len(channels.sent) != 4 || stats.ChannelFiresRetried != 1 {
		t.Fatalf("the deferred trigger should be delivered: stats=%+v attempts=%d err=%v", stats, len(channels.sent), err)
	}
	if got := channels.sent[2].target; got.RoutingKey != originalKey || got.Region != "us" {
		t.Fatal("the resolve must use the original PagerDuty routing key and region")
	}
	resolved, fired := channels.sent[2].msg, channels.sent[3].msg
	if resolved.Event != alertchannels.EventResolve || fired.Event != alertchannels.EventFire ||
		resolved.IdempotencyKey != channels.sent[1].msg.IdempotencyKey || resolved.DedupKey != fired.DedupKey || fired.DedupKey != channels.sent[0].msg.DedupKey {
		t.Fatalf("want a stable resolve before reopening the PagerDuty key, got resolve=%+v fire=%+v", resolved, fired)
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
