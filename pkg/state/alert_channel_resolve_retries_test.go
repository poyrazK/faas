package state_test

// adr: 20261031144636 — a channel's undelivered recovery notice keeps what
// its first attempt reported, is due only once its backoff passes and while
// its rule is recovered, and goes with its rule, its channel, or a new fire.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type channelRetryStore interface {
	state.AlertChannelResolveRetryStore
	CreateAlertRule(ctx context.Context, r state.AlertRule) (state.AlertRule, error)
	DeleteAlertRule(ctx context.Context, id string) error
	SetAlertRuleState(ctx context.Context, id string, to state.AlertState, at time.Time) (bool, error)
	CreateNotificationChannel(ctx context.Context, c state.NotificationChannel, limit int) (state.NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, accountID, id string) error
}

func checkChannelResolveRetries(t *testing.T, ctx context.Context, s channelRetryStore, rule state.AlertRule) {
	t.Helper()
	newChannel := func(name string) state.NotificationChannel {
		t.Helper()
		ch, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: rule.AccountID, Name: name, Kind: "email", Email: "ops@example.com"}, api.MaxNotificationChannelsPerAccount)
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	due := func(now time.Time) []state.AlertChannelResolveRetry {
		t.Helper()
		rows, err := s.DueAlertChannelResolveRetries(ctx, now, 10)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	record := func(channelID string, next time.Time) {
		t.Helper()
		if err := s.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{RuleID: rule.ID, ChannelID: channelID,
			Observed: 2.5, ResolvedAt: at, NextAttemptAt: next, LastError: "slack returned 503"}); err != nil {
			t.Fatal(err)
		}
	}

	ch := newChannel("ops")
	record(ch.ID, at.Add(time.Minute))
	if got := due(at); len(got) != 0 {
		t.Fatalf("due before its backoff = %+v", got)
	}
	got := due(at.Add(time.Minute))
	if len(got) != 1 || got[0].ChannelID != ch.ID || got[0].Observed != 2.5 || !got[0].ResolvedAt.Equal(at) || got[0].Attempts != 1 {
		t.Fatalf("due = %+v; want the first attempt's notice", got)
	}
	// A second failure keeps what the first reported and moves the next try.
	if err := s.RecordAlertChannelResolveRetry(ctx, state.AlertChannelResolveRetry{RuleID: rule.ID, ChannelID: ch.ID,
		Observed: 9, ResolvedAt: at.Add(time.Hour), NextAttemptAt: at.Add(3 * time.Minute), LastError: "again"}); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(2 * time.Minute)); len(got) != 0 {
		t.Fatalf("due before the moved backoff = %+v", got)
	}
	if got := due(at.Add(3 * time.Minute)); len(got) != 1 || got[0].Attempts != 2 || got[0].Observed != 2.5 || !got[0].ResolvedAt.Equal(at) {
		t.Fatalf("due after a second failure = %+v", got)
	}
	// Only a recovered rule's notices are due.
	if _, err := s.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, at); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due while the rule fires = %+v", got)
	}
	if _, err := s.SetAlertRuleState(ctx, rule.ID, state.AlertStateOk, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearAlertChannelResolveRetries(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due after clearing = %+v", got)
	}

	record(ch.ID, at)
	if err := s.DeleteNotificationChannel(ctx, rule.AccountID, ch.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due after the channel was deleted = %+v", got)
	}
	other := newChannel("pager")
	record(other.ID, at)
	if err := s.DeleteAlertChannelResolveRetry(ctx, rule.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due after the retry was deleted = %+v", got)
	}
	record(other.ID, at)
	if err := s.DeleteAlertRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if got := due(at.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("due after the rule was deleted = %+v", got)
	}
}

func TestMemStoreChannelResolveRetries(t *testing.T) {
	m, ctx, acct, app := alertFixture(t)
	rule, err := m.CreateAlertRule(ctx, memSampleRule(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	checkChannelResolveRetries(t, ctx, m, rule)
}

func TestPgStore_ChannelResolveRetries(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app, _ := seedLiveDeploy(t, s, ctx)
	rule, err := s.CreateAlertRule(ctx, pgSampleAlertRule(acct, app))
	if err != nil {
		t.Fatal(err)
	}
	checkChannelResolveRetries(t, ctx, s, rule)
}
