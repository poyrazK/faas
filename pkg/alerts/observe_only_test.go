package alerts_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type observationTestAction func()

func (f observationTestAction) Execute(context.Context, state.AlertRule, float64, time.Time) error {
	f()
	return nil
}

type scopedObservationProm struct {
	*anomalyProm
	production, staging *anomalyProm
}

func (p *scopedObservationProm) source(query string) (*anomalyProm, error) {
	if !strings.Contains(query, "gateway_environment_request_duration_seconds") {
		return nil, fmt.Errorf("expected scoped telemetry: %s", query)
	}
	switch {
	case strings.Contains(query, `scope="staging"`):
		return p.staging, nil
	case strings.Contains(query, `scope="production"`):
		return p.production, nil
	default:
		return nil, fmt.Errorf("missing environment scope: %s", query)
	}
}

func (p *scopedObservationProm) QueryVectorStrictAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error) {
	source, err := p.source(query)
	if err != nil {
		return nil, err
	}
	return source.QueryVectorStrictAt(ctx, query, at)
}

func (p *scopedObservationProm) QueryRangeStrict(ctx context.Context, query, start, end, step string) ([]promql.QueryRangeSeries, error) {
	source, err := p.source(query)
	if err != nil {
		return nil, err
	}
	return source.QueryRangeStrict(ctx, query, start, end, step)
}

func TestObserveOnlyScopesTrafficAndBaselines(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	staging, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	staging, err := store.UpdateAlertRule(ctx, staging.ID, state.UpdateAlertRuleParams{
		ObserveOnly: new(true), EnvironmentScope: new("staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID: staging.AccountID, AppID: staging.AppID, Name: "Observe production",
		Enabled: true, ObserveOnly: true, EnvironmentScope: "production",
		Metric: staging.Metric, Comparison: staging.Comparison, Threshold: staging.Threshold,
		WindowSpec: staging.WindowSpec, CooldownMinutes: staging.CooldownMinutes,
		WebhookURL: staging.WebhookURL, WebhookSecretSealed: staging.WebhookSecretSealed,
	})
	if err != nil {
		t.Fatal(err)
	}
	prom := &scopedObservationProm{
		anomalyProm: &anomalyProm{},
		production:  &anomalyProm{currentSamples: 400, currentValue: 4, pastSamples: 400, pastValue: 4, trafficDays: 7},
		staging:     &anomalyProm{currentSamples: 400, currentValue: 10, pastSamples: 400, pastValue: 1, trafficDays: 6},
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	now := production.UpdatedAt.Add(time.Minute)
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
		Identity:   func() *age.X25519Identity { return ident },
		Now:        func() time.Time { return now },
		ActionExec: observationTestAction(func() { t.Error("Observe only executed an action") }),
	})
	stats, err := evaluator.RunOnce(ctx)
	if err != nil || stats.Fired != 0 || dispatch.callCount() != 0 {
		t.Fatalf("observe-only side effects: stats=%+v err=%v deliveries=%d", stats, err, dispatch.callCount())
	}
	for _, tc := range []struct {
		rule      state.AlertRule
		source    *anomalyProm
		baseline  float64
		days      int
		incidents int
	}{{staging, prom.staging, 1, 6, 1}, {production, prom.production, 4, 7, 0}} {
		got, err := store.AlertRuleByID(ctx, tc.rule.ID)
		evidence := got.AnomalyEvaluation
		if err != nil || len(got.ObservedIncidents) != tc.incidents || evidence.Baseline == nil || *evidence.Baseline != tc.baseline ||
			evidence.BaselineDays == nil || *evidence.BaselineDays != tc.days ||
			tc.source.exactQueries.Load() != 2 {
			t.Fatalf("scope=%s evidence=%+v incidents=%+v reads=%d err=%v", tc.rule.EnvironmentScope, evidence, got.ObservedIncidents, tc.source.exactQueries.Load(), err)
		}
		if tc.incidents > 0 && got.ObservedIncidents[0].EnvironmentScope != tc.rule.EnvironmentScope {
			t.Fatalf("incorrect incident environment: %+v", got.ObservedIncidents[0])
		}
	}

	sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte("https://hooks.slack.com/services/T1/B2/test"), 512)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{
		AccountID: staging.AccountID, Name: "Scoped ops", Kind: alertchannels.KindSlack, TargetSealed: sealed,
	}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, staging.ID, []string{channel.ID}); err != nil {
		t.Fatal(err)
	}
	for _, rule := range []state.AlertRule{staging, production} {
		saved, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{
			ObserveOnly: new(false), NotifyResolved: new(true),
		})
		if err != nil || saved.ObserveOnly || saved.EnvironmentScope != rule.EnvironmentScope {
			t.Fatalf("enable Notify for %q: saved=%+v err=%v", rule.EnvironmentScope, saved, err)
		}
	}
	channels := &recordingChannels{}
	evaluator.SetChannels(channels, nil)
	tick := func(value float64, wantedFires, wantedMessages int) {
		t.Helper()
		now = now.Add(time.Minute)
		prom.staging.currentValue = value
		stats, err := evaluator.RunOnce(ctx)
		if err != nil || stats.Fired != wantedFires || dispatch.callCount() != wantedMessages || len(channels.sent) != wantedMessages {
			t.Fatalf("staging value=%g: stats=%+v err=%v webhooks=%d channels=%d", value, stats, err, dispatch.callCount(), len(channels.sent))
		}
	}
	tick(10, 1, 1)
	tick(10, 0, 1)
	stillFiring, err := store.AlertRuleByID(ctx, staging.ID)
	if err != nil || stillFiring.AnomalyEvaluation.Reason != api.AlertAnomalyNotificationCooldown {
		t.Fatalf("sustained breach did not report cooldown: %+v err=%v", stillFiring.AnomalyEvaluation, err)
	}
	tick(1, 0, 2)
	for i, event := range []string{"alert.fired", "alert.resolved"} {
		payload, notification := dispatch.calls[i].Payload, channels.sent[i].msg
		if payload["event"] != event || payload["environment_scope"] != "staging" || payload["rule_id"] != staging.ID ||
			notification.EnvironmentScope != "staging" || notification.DedupKey != staging.ID ||
			!strings.Contains(alertchannels.Headline(notification), "[staging]") {
			t.Fatalf("unexpected scoped %s payload=%v notification=%+v", event, payload, notification)
		}
	}
	quiet, err := store.AlertRuleByID(ctx, production.ID)
	if err != nil || quiet.State != state.AlertStateOk || !quiet.LastFiredAt.IsZero() {
		t.Fatalf("production adopted staging breach: state=%s last fire=%s err=%v", quiet.State, quiet.LastFiredAt, err)
	}
}

func TestObserveOnlyTracksIncidentsAndPromotesWithoutCooldown(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
	setMode := func(observeOnly bool) state.AlertRule {
		t.Helper()
		updated, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: &observeOnly})
		if err != nil || updated.ObserveOnly != observeOnly || !updated.Enabled {
			t.Fatalf("set mode=%v: %+v err=%v", observeOnly, updated, err)
		}
		return updated
	}
	rule = setMode(true)
	rule, err := store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Action: new("demote")})
	if err != nil {
		t.Fatal(err)
	}
	start := rule.UpdatedAt.UTC().Truncate(time.Second).Add(time.Minute)
	now := start
	cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
	prom := &anomalyProm{currentSamples: 400, currentValue: 5, pastSamples: 400, pastValue: 1, trafficDays: 7}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
	actions := 0
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
		Identity: func() *age.X25519Identity { return ident }, Now: func() time.Time { return now },
		ActionExec: observationTestAction(func() { actions++ }),
	})
	tick := func(after time.Duration, samples, value float64, fires int) state.AlertRule {
		t.Helper()
		now = start.Add(after)
		prom.currentSamples, prom.currentValue = samples, value
		stats, err := ev.RunOnce(ctx)
		if err != nil || stats.Fired != fires {
			t.Fatalf("tick at %s: stats=%+v err=%v, want %d live fires", after, stats, err, fires)
		}
		got, err := store.AlertRuleByID(ctx, rule.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := tick(0, 400, 5, 0)
	if len(got.ObservedIncidents) != 1 || !got.ObservedIncidents[0].OpenedAt.Equal(start) ||
		got.ObservedIncidents[0].EligibleNotifications != 1 || got.AnomalyEvaluation.Reason != api.AlertAnomalyObservedBreach ||
		!got.LastFiredAt.IsZero() || got.State != state.AlertStateOk {
		t.Fatalf("first observation: %+v", got)
	}
	opening := got.ObservedIncidents[0].OpeningEvidence
	if opening == nil || opening.Observed != 5 || opening.Baseline != 1 || opening.TriggerValue != 3 || opening.BaselineDays != 7 {
		t.Fatalf("first breach did not save its evidence: %+v", opening)
	}
	got = tick(time.Minute, 400, 5, 0)
	if len(got.ObservedIncidents) != 1 || got.ObservedIncidents[0].EligibleNotifications != 1 {
		t.Fatalf("continued breach inside cooldown: %+v", got.ObservedIncidents)
	}
	got = tick(2*time.Minute, -1, 5, 0)
	if got.ObservedIncidents[0].EndedAt != nil || got.AnomalyEvaluation.Reason != api.AlertAnomalyTelemetryMissing {
		t.Fatalf("missing data closed the observed incident: %+v", got)
	}
	got = tick(cooldown, 400, 5, 0)
	if got.ObservedIncidents[0].EligibleNotifications != 2 {
		t.Fatalf("continued breach at cooldown deadline: %+v", got.ObservedIncidents)
	}
	got = tick(cooldown+time.Minute, 400, 1, 0)
	if got.ObservedIncidents[0].Outcome != api.AlertObservationRecovered || got.ObservedIncidents[0].EndedAt == nil {
		t.Fatalf("recovery was not recorded: %+v", got.ObservedIncidents)
	}
	tick(cooldown+2*time.Minute, 400, 5, 0)
	if deliveries, err := store.ListAlertDeliveriesForRule(ctx, rule.ID, 10, true); err != nil || len(deliveries) != 0 || dispatch.callCount() != 0 || actions != 0 {
		t.Fatalf("observe-only side effects: deliveries=%v calls=%d actions=%d err=%v", deliveries, dispatch.callCount(), actions, err)
	}
	got = setMode(false)
	if got.ObservedIncidents[0].Outcome != api.AlertObservationEnded || got.ObservedIncidents[0].EndedAt == nil {
		t.Fatalf("promotion must end the observation: %+v", got.ObservedIncidents)
	}
	tick(cooldown+3*time.Minute, 400, 5, 1)
	setMode(true)
	tick(cooldown+4*time.Minute, 400, 5, 0)
	setMode(false)
	tick(cooldown+5*time.Minute, 400, 5, 1)
	tick(cooldown+6*time.Minute, 400, 5, 0)
	if dispatch.callCount() != 2 || actions != 2 {
		t.Fatalf("live webhook calls=%d, actions=%d, want 2 of each", dispatch.callCount(), actions)
	}
}

func TestAnomalyTwoCheckRecoverySurvivesRestart(t *testing.T) {
	for _, mode := range []struct {
		name        string
		observeOnly bool
	}{{"notify", false}, {"observe", true}} {
		t.Run(mode.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, identity, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertAboveBaseline, 3)
			rule, err := store.UpdateAlertRule(t.Context(), rule.ID, state.UpdateAlertRuleParams{
				ObserveOnly: new(mode.observeOnly), NotifyResolved: new(true),
				RecoveryConfirmationChecks: new(2),
			})
			if err != nil {
				t.Fatal(err)
			}
			prom := &anomalyProm{pastSamples: 400, pastValue: 1, trafficDays: 7}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
			now := rule.UpdatedAt.Add(time.Minute)
			for _, tick := range []struct {
				samples, value float64
				open           bool
				reason         api.AlertAnomalyEvaluationReason
				count          int64
			}{
				{400, 5, true, "", 0},
				{400, 1, true, api.AlertAnomalyRecoveryPending, 1},
				{-1, 1, true, api.AlertAnomalyTelemetryMissing, 0},
				{400, 1, true, api.AlertAnomalyRecoveryPending, 1},
				{400, 1, false, api.AlertAnomalyRecoveryConfirmed, 2},
				{400, 1, false, api.AlertAnomalyThresholdNotReached, 0},
			} {
				prom.currentSamples, prom.currentValue = tick.samples, tick.value
				evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{
					Store: store, PromQL: prom, Dispatcher: dispatch, Log: discardLog(),
					Identity: func() *age.X25519Identity { return identity },
					Now:      func() time.Time { return now },
				})
				if _, err := evaluator.RunOnce(t.Context()); err != nil {
					t.Fatal(err)
				}
				got, err := store.AlertRuleByID(t.Context(), rule.ID)
				if err != nil {
					t.Fatal(err)
				}
				open, err := store.AlertRuleEpisodeOpen(t.Context(), rule.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantReason := tick.reason
				if mode.observeOnly {
					open = len(got.ObservedIncidents) == 1 && got.ObservedIncidents[0].EndedAt == nil
					if wantReason == "" {
						wantReason = api.AlertAnomalyObservedBreach
					}
				}
				if open != tick.open || got.AnomalyEvaluation.Reason != wantReason || got.AnomalyEvaluation.Count != tick.count {
					t.Fatalf("at %s: open=%v evaluation=%+v, want open=%v reason=%s count=%d", now, open, got.AnomalyEvaluation, tick.open, wantReason, tick.count)
				}
				wantMessages := 0
				if !mode.observeOnly {
					wantMessages = 1
					if !tick.open {
						wantMessages = 2
					}
				}
				if dispatch.callCount() != wantMessages {
					t.Fatalf("at %s: got %d notifications, want %d", now, dispatch.callCount(), wantMessages)
				}
				now = now.Add(time.Minute)
			}
			if !mode.observeOnly && dispatch.calls[1].Payload["event"] != "alert.resolved" {
				t.Fatalf("last notification was not a recovery: %+v", dispatch.calls[1])
			}
		})
	}
}
