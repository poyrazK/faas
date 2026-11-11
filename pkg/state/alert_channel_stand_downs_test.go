package state_test

// adr: 20261104142830 — a rule whose episode ends without it recovering
// owes its bound channels a recovery notice: queued by the evaluator for a
// rule it stops evaluating, and by the delete trigger for a deleted rule,
// which survives the rule. A rule that never fired owes nothing. Channels
// can be renamed or given a new destination in place.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standDownStore interface {
	state.AlertChannelStandDownStore
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
