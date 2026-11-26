package state_test

// adr: 20261104142830 — a rule whose episode ends without it recovering
// owes its bound channels a recovery notice: queued by the evaluator for a
// rule it stops evaluating, and by the delete trigger for a deleted rule,
// which survives the rule. A rule that never fired owes nothing. Channels
// can be renamed or given a new destination in place.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standDownStore interface {
	state.AlertChannelStandDownStore
	state.AlertWebhookStandDownStore
	state.AlertChannelResolveRetryStore
	state.NotificationChannelUpdateStore
	CreateAlertRule(ctx context.Context, r state.AlertRule) (state.AlertRule, error)
	UpdateAlertRule(ctx context.Context, id string, p state.UpdateAlertRuleParams) (state.AlertRule, error)
	DeleteAlertRule(ctx context.Context, id string) error
	SetAlertRuleState(ctx context.Context, id string, to state.AlertState, at time.Time) (bool, error)
	SetAlertRuleChannels(ctx context.Context, ruleID string, channelIDs []string) error
	CreateNotificationChannel(ctx context.Context, c state.NotificationChannel, limit int) (state.NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, accountID, id string) (state.NotificationChannel, error)
	RecordNotificationChannelDelivery(ctx context.Context, id string, at time.Time, errMsg string) error
}

func checkStandDowns(t *testing.T, ctx context.Context, s standDownStore, template state.AlertRule) {
	t.Helper()
	ch, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: template.AccountID, Name: "pager", Kind: "pagerduty", TargetSealed: []byte("sealed-key"), TargetHint: "…abcd", PagerDutyRegion: "us"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	newRule := func(name string) state.AlertRule {
		t.Helper()
		r := template
		r.ID, r.Name = "", name
		created, err := s.CreateAlertRule(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetAlertRuleChannels(ctx, created.ID, []string{ch.ID}); err != nil {
			t.Fatal(err)
		}
		return created
	}
	now := time.Now().UTC()
	due := func() []state.AlertChannelStandDown {
		t.Helper()
		rows, err := s.DueAlertChannelStandDowns(ctx, now.Add(time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}

	// A firing rule that stops being evaluated owes its channel a notice.
	firing := newRule("firing")
	if _, err := s.SetAlertRuleState(ctx, firing.ID, state.AlertStateFiring, now); err != nil {
		t.Fatal(err)
	}
	if open, err := s.AlertRuleEpisodeOpen(ctx, firing.ID); err != nil || !open {
		t.Fatalf("episode open = %v, %v", open, err)
	}
	disabled := false
	if _, err := s.UpdateAlertRule(ctx, firing.ID, state.UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	stranded, err := s.ListStrandedAlertRules(ctx, 10)
	if err != nil || len(stranded) != 1 || stranded[0].ID != firing.ID {
		t.Fatalf("stranded = %+v, %v; want the disabled firing rule", stranded, err)
	}
	if n, err := s.QueueAlertChannelStandDowns(ctx, firing.ID, state.StandDownDisabled, now); err != nil || n != 1 {
		t.Fatalf("queued = %d, %v; want one notice", n, err)
	}
	if n, err := s.QueueAlertChannelStandDowns(ctx, firing.ID, state.StandDownDisabled, now); err != nil || n != 0 {
		t.Fatalf("queued again = %d, %v; a channel keeps the notice it is owed", n, err)
	}
	got := due()
	if len(got) != 1 || got[0].RuleID != firing.ID || got[0].ChannelID != ch.ID || got[0].Reason != state.StandDownDisabled || got[0].RuleName != "firing" {
		t.Fatalf("due = %+v", got)
	}
	if err := s.RecordAlertChannelStandDownAttempt(ctx, firing.ID, ch.ID, now.Add(2*time.Hour), "pagerduty returned 503"); err != nil {
		t.Fatal(err)
	}
	if got := due(); len(got) != 0 {
		t.Fatalf("due before its backoff = %+v", got)
	}
	if err := s.DeleteAlertChannelStandDown(ctx, firing.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAlertRuleState(ctx, firing.ID, state.AlertStateOk, now); err != nil {
		t.Fatal(err)
	}

	for _, prior := range []struct {
		name string
		open bool
	}{{"active", true}, {"pending resolve", false}} {
		rule := newRule(prior.name)
		if prior.open {
			if _, err := s.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, now); err != nil {
				t.Fatal(err)
			}
		} else if err := s.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{
			RuleID: rule.ID, ChannelID: ch.ID, ResolvedAt: now, NextAttemptAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{
			Metric: new(state.AlertMetricErrorRate), Comparison: new(state.AlertAboveBaseline),
			Threshold: new(3.0), Name: new("observing " + prior.name), ObserveOnly: new(true),
		}); err != nil {
			t.Fatal(err)
		}
		if active, err := s.AlertRuleEpisodeOpen(ctx, rule.ID); err != nil || active {
			t.Fatalf("observed rule has a live episode: %v, %v", active, err)
		}
		if got := due(); len(got) != 1 || got[0].RuleID != rule.ID || got[0].ChannelID != ch.ID || got[0].RuleName != prior.name {
			t.Fatalf("mode change lost the notice for the original destination: %+v", got)
		}
		if retries, err := s.DueAlertChannelResolveRetries(ctx, now.Add(time.Hour), 10); err != nil || len(retries) != 0 {
			t.Fatalf("mode change did not transfer the old resolve retry: %+v, %v", retries, err)
		}
		if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)}); err != nil {
			t.Fatal(err)
		}
		if got := due(); len(got) != 1 || got[0].RuleID != rule.ID {
			t.Fatalf("switching back lost the pending stand-down: %+v", got)
		}
		if pending, err := s.AlertChannelStandDownPending(ctx, rule.ID, ch.ID, true); err != nil || !pending {
			t.Fatalf("a refire must preserve the old stand-down: pending=%v err=%v", pending, err)
		}
		if err := s.DeleteAlertChannelStandDown(ctx, rule.ID, ch.ID); err != nil {
			t.Fatal(err)
		}
	}

	// A rule that never fired owes nothing, deleted or not.
	quiet := newRule("quiet")
	if n, err := s.QueueAlertChannelStandDowns(ctx, quiet.ID, state.StandDownNotReleased, now); err != nil || n != 0 {
		t.Fatalf("a rule that never fired queued %d, %v", n, err)
	}
	if err := s.DeleteAlertRule(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(); len(got) != 0 {
		t.Fatalf("deleting a rule that never fired queued %+v", got)
	}

	// A rule deleted while firing still owes its channel, after it is gone.
	gone := newRule("gone")
	if _, err := s.SetAlertRuleState(ctx, gone.ID, state.AlertStateFiring, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAlertRule(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(); len(got) != 1 || got[0].RuleID != gone.ID || got[0].Reason != state.StandDownDeleted || got[0].RuleName != "gone" {
		t.Fatalf("due after deleting a firing rule = %+v", got)
	}
	if err := s.ClearAlertChannelStandDowns(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	// A resolve still being retried moves with the rule's stand-down.
	retried := newRule("retried")
	if err := s.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{RuleID: retried.ID, ChannelID: ch.ID, Observed: 1, ResolvedAt: now, NextAttemptAt: now, LastError: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAlertRule(ctx, retried.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(); len(got) != 1 || got[0].RuleID != retried.ID {
		t.Fatalf("due after deleting a rule with a pending resolve = %+v", got)
	}

	t.Run("pre-edit webhook snapshot", func(t *testing.T) {
		live := newRule("webhook live")
		live, err := s.UpdateAlertRule(ctx, live.ID, state.UpdateAlertRuleParams{
			NotifyResolved: new(true), WebhookURL: new("https://example.com/live"), WebhookSecretSealed: new([]byte("live sealed key")),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetAlertRuleState(ctx, live.ID, state.AlertStateFiring, now); err != nil {
			t.Fatal(err)
		}
		changed, err := s.UpdateAlertRule(ctx, live.ID, state.UpdateAlertRuleParams{
			ObserveOnly: new(true), Metric: new(state.AlertMetricErrorRate), Comparison: new(state.AlertAboveBaseline),
			Threshold: new(3.0), Name: new("webhook observation"), EnvironmentScope: new("staging"),
			NotifyResolved: new(false), WebhookURL: new("https://example.com/observation"), WebhookSecretSealed: new([]byte("new sealed key")),
		})
		if err != nil {
			t.Fatal(err)
		}
		notices, err := s.DueAlertWebhookStandDowns(ctx, now.Add(time.Hour), 10)
		if err != nil || len(notices) != 1 {
			t.Fatalf("queued webhook stand-downs=%d err=%v, want 1", len(notices), err)
		}
		notice := notices[0]
		if notice.Rule.ID != live.ID || notice.Rule.Name != live.Name || notice.Rule.WebhookURL != live.WebhookURL ||
			string(notice.Rule.WebhookSecretSealed) != string(live.WebhookSecretSealed) || !notice.QueuedAt.Equal(changed.UpdatedAt) ||
			notice.Rule.EnvironmentScope != live.EnvironmentScope || notice.Rule.Comparison != live.Comparison || notice.Rule.Threshold != live.Threshold {
			t.Fatal("queued webhook did not snapshot the original live rule")
		}
		if err := s.RetryAlertWebhookStandDown(ctx, notice.ID, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if notices, err := s.DueAlertWebhookStandDowns(ctx, now.Add(time.Hour), 10); err != nil || len(notices) != 0 {
			t.Fatalf("webhook notices=%d before backoff, err=%v", len(notices), err)
		}
		if notices, err := s.DueAlertWebhookStandDowns(ctx, now.Add(3*time.Hour), 10); err != nil || len(notices) != 1 || notices[0].Attempts != 1 {
			t.Fatalf("webhook notices=%d after backoff, err=%v", len(notices), err)
		}
		if err := s.DeleteAlertWebhookStandDown(ctx, notice.ID); err != nil {
			t.Fatal(err)
		}
	})

	checkChannelUpdate(t, ctx, s, ch)
}

func checkChannelUpdate(t *testing.T, ctx context.Context, s standDownStore, ch state.NotificationChannel) {
	t.Helper()
	if err := s.RecordNotificationChannelDelivery(ctx, ch.ID, time.Now().UTC(), "pagerduty returned 400"); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: ch.AccountID, Name: "taken", Kind: "email", Email: "ops@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	taken := other.Name
	if _, err := s.UpdateNotificationChannel(ctx, ch.AccountID, ch.ID, state.UpdateNotificationChannelParams{Name: &taken}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rename onto a taken name = %v; want ErrConflict", err)
	}
	name, hint, region := "oncall", "…wxyz", "eu"
	got, err := s.UpdateNotificationChannel(ctx, ch.AccountID, ch.ID, state.UpdateNotificationChannelParams{
		Name: &name, TargetSealed: []byte("new-sealed-key"), TargetHint: &hint, PagerDutyRegion: &region,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ch.ID || got.Name != "oncall" || string(got.TargetSealed) != "new-sealed-key" || got.TargetHint != hint || got.PagerDutyRegion != "eu" || got.LastError != "" {
		t.Fatalf("updated = %+v; want the new name and destination, the last error cleared", got)
	}
	if _, err := s.UpdateNotificationChannel(ctx, ch.AccountID, "6f0c1d4e-7a52-4c37-9a63-0f6a7b6c1a10", state.UpdateNotificationChannelParams{Name: &name}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("update of a missing channel = %v; want ErrNotFound", err)
	}
}

func TestMemStoreStandDowns(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	checkStandDowns(t, ctx, m, memSampleRule(acct.ID, app.ID))
}

func TestPgStore_StandDowns(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	checkStandDowns(t, ctx, s, pgSampleAlertRule(acct, app))
}

func TestPgStore_AlertScopeDeliveryGuard(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule := pgSampleAlertRule(acct, app)
	rule.Metric, rule.Comparison, rule.Threshold = state.AlertMetricRequestCount, state.AlertAboveBaseline, 3
	rule.EnvironmentScope, rule.NotifyResolved = "staging", true
	rule, err := s.CreateAlertRule(ctx, rule)
	if err != nil {
		t.Fatal(err)
	}
	pagerDutyChannel := func(name string) state.NotificationChannel {
		t.Helper()
		channel, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{
			AccountID: acct, Name: name, Kind: "pagerduty", PagerDutyRegion: "us", TargetSealed: []byte(name),
		}, api.MaxNotificationChannelsPerAccount)
		if err != nil {
			t.Fatal(err)
		}
		return channel
	}
	original := pagerDutyChannel("original")
	if err := s.SetAlertRuleChannels(ctx, rule.ID, []string{original.ID}); err != nil {
		t.Fatal(err)
	}

	insert := func(key, payload string, test bool) error {
		// The old meterd claims a delivery with this insert and doesn't
		// read the rule's scope. Exercise the database guard directly.
		_, err := pool.Exec(ctx, `
			INSERT INTO alert_deliveries (rule_id, account_id, app_id, idempotency_key, payload, status, observed_value, fired_at, is_test)
			SELECT id, account_id, app_id, $2, $3::jsonb, 'pending', 100, now(), $4 FROM alert_rules WHERE id = $1
		`, rule.ID, key, payload, test)
		return err
	}
	reject := func(key, payload, constraint string) {
		t.Helper()
		err := insert(key, payload, false)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
			t.Fatalf("live insert %s: got %v, want constraint %s", key, err, constraint)
		}
	}
	reject("legacy", `{}`, "alert_deliveries_live_scope")
	reject("other-scope", `{"environment_scope":"production"}`, "alert_deliveries_live_scope")
	if err := insert("explicit-test", `{}`, true); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"environment_scope":"staging","event":"alert.fired"}`)
	first := rule.UpdatedAt.Add(time.Minute)
	if _, won, _, err := s.ClaimAlertFire(ctx, rule.ID, "scoped-fire", payload, 100, first); err != nil || !won {
		t.Fatalf("scoped fire: won=%v err=%v", won, err)
	}
	if open, err := s.AlertRuleEpisodeOpen(ctx, rule.ID); err != nil || !open {
		t.Fatalf("claimed episode open=%v err=%v", open, err)
	}
	if tag, err := pool.Exec(ctx, `UPDATE alert_rules SET state = 'ok' WHERE id = $1`, rule.ID); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("legacy app-wide state update affected %d scoped rules, err=%v", tag.RowsAffected(), err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(true)}); err != nil {
		t.Fatal(err)
	}
	reject("observe-only", string(payload), "alert_deliveries_live_scope")
	if _, err := s.UpdateNotificationChannel(ctx, acct, original.ID, state.UpdateNotificationChannelParams{
		TargetSealed: []byte("rotated"), PagerDutyRegion: new("eu"),
	}); err != nil {
		t.Fatal(err)
	}
	alias := pagerDutyChannel("alias")
	if err := s.SetAlertRuleChannels(ctx, rule.ID, []string{alias.ID}); err != nil {
		t.Fatal(err)
	}
	older, err := s.ListPagerDutyObservationStandDownChannels(ctx, rule.ID)
	if err != nil || len(older) != 1 || older[0].ID != original.ID || string(older[0].TargetSealed) != "original" || older[0].PagerDutyRegion != "us" {
		t.Fatalf("expected the old unbound stand-down channel, got %d channels and %v", len(older), err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)}); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Minute)
	_, won, recipients, err := s.ClaimAlertFire(ctx, rule.ID, "new-episode", payload, 100, second)
	if err != nil || !won {
		t.Fatalf("new scoped fire: won=%v err=%v", won, err)
	}
	if recipients.Held.WebhookURL != rule.WebhookURL || !recipients.Held.ChannelIDs[alias.ID] ||
		len(recipients.Channels) != 1 || recipients.Channels[0].ID != alias.ID {
		t.Fatal("the claim should return the alias and defer it and the webhook")
	}
	fires, err := s.DueDeferredAlertFires(ctx, second, 10)
	webhook := slices.IndexFunc(fires, func(fire state.AlertDeferredFire) bool { return fire.WebhookURL != "" })
	if err != nil || len(fires) != 2 || webhook < 0 || !fires[webhook].Blocked {
		t.Fatalf("deferred fires=%d err=%v, want the possible alias and a blocked webhook", len(fires), err)
	}
	if started, err := s.StartDeferredWebhookFire(ctx, fires[webhook].ID); err != nil || started {
		t.Fatalf("blocked webhook started=%v err=%v", started, err)
	}
	notices, err := s.DueAlertWebhookStandDowns(ctx, second, 10)
	if err != nil || len(notices) != 1 {
		t.Fatalf("stand-down notices=%d err=%v", len(notices), err)
	}
	if err := s.DeleteAlertWebhookStandDown(ctx, notices[0].ID); err != nil {
		t.Fatal(err)
	}
	if started, err := s.StartDeferredWebhookFire(ctx, fires[webhook].ID); err != nil || !started {
		t.Fatalf("unblocked webhook started=%v err=%v", started, err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(true)}); err != nil {
		t.Fatal(err)
	}
	notices, err = s.DueAlertWebhookStandDowns(ctx, second.Add(time.Minute), 10)
	if err != nil || len(notices) != 1 {
		t.Fatalf("stand-down notices after dispatch starts=%d err=%v", len(notices), err)
	}
	// App-wide rules match a legacy claim's empty payload. Even then, the
	// evaluator must understand the pending handoff before claiming a fire.
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{EnvironmentScope: new("")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{ObserveOnly: new(false)}); err != nil {
		t.Fatal(err)
	}
	reject("legacy-unscoped", "{}", "alert_deliveries_live_handoff")
	if err := insert("unscoped-explicit-test", "{}", true); err != nil {
		t.Fatal(err)
	}
	if _, won, _, err := s.ClaimAlertFire(ctx, rule.ID, "handoff-aware-unscoped", nil, 100, second.Add(time.Minute)); err != nil || !won {
		t.Fatalf("handoff-aware app-wide fire: won=%v err=%v", won, err)
	}

}
