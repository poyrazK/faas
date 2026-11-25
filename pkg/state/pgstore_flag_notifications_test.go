package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261125085913: an environment's channels are replaced in place, a
// notification key queues once, due rows are leased once with their plan
// and count an attempt, a retry waits for its time, and settled rows sweep.
func TestPg_FlagNotifications(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "flag-notifications")
	s, ctx, acct := f.s, f.ctx, f.acct
	now := time.Now().UTC().Truncate(time.Second)
	ch, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct, Name: "ops", Kind: "slack",
		TargetSealed: []byte("sealed"), TargetHint: "hooks.slack.com/…/abcd"}, 10)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}

	if _, err := s.FlagNotificationSettings(ctx, f.env); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("settings before set = %v; want ErrNotFound", err)
	}
	set, err := s.SetFlagNotificationSettings(ctx, state.FlagNotificationSettings{EnvironmentID: f.env, AccountID: acct, ChannelIDs: []string{ch.ID}, UpdatedBy: acct})
	if err != nil || len(set.ChannelIDs) != 1 || set.ChannelIDs[0] != ch.ID {
		t.Fatalf("set = %+v, %v", set, err)
	}
	if _, err := s.SetFlagNotificationSettings(ctx, state.FlagNotificationSettings{EnvironmentID: f.env, AccountID: acct, ChannelIDs: nil, UpdatedBy: acct}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, err := s.FlagNotificationSettings(ctx, f.env); err != nil || len(got.ChannelIDs) != 0 {
		t.Fatalf("cleared settings = %+v, %v", got, err)
	}

	n := state.FlagNotification{AccountID: acct, EnvironmentID: f.env, Kind: state.FlagNotifyExperimentDecision,
		Key: "experiment:x:ship_variant:b", Subject: "Ship b", Summary: "b wins", ChannelIDs: []string{ch.ID}}
	if queued, err := s.EnqueueFlagNotification(ctx, n, now); err != nil || !queued {
		t.Fatalf("enqueue = %v, %v", queued, err)
	}
	if queued, err := s.EnqueueFlagNotification(ctx, n, now); err != nil || queued {
		t.Fatalf("enqueue again = %v, %v; want not queued", queued, err)
	}
	claimed, err := s.ClaimDueFlagNotifications(ctx, now, now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].Plan == "" || claimed[0].Attempts != 1 || claimed[0].Summary != "b wins" {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, err := s.ClaimDueFlagNotifications(ctx, now, now.Add(time.Minute), 10); err != nil || len(again) != 0 {
		t.Fatalf("second claim = %+v, %v; want none while leased", again, err)
	}
	retry := now.Add(5 * time.Minute)
	if err := s.SettleFlagNotification(ctx, state.FlagNotificationSettle{ID: claimed[0].ID, Status: state.FlagNotificationPending,
		LastError: "slack 500", NextAttemptAt: retry, Now: now}); err != nil {
		t.Fatalf("settle retry: %v", err)
	}
	if early, err := s.ClaimDueFlagNotifications(ctx, now.Add(time.Minute), now.Add(2*time.Minute), 10); err != nil || len(early) != 0 {
		t.Fatalf("claim before retry = %+v, %v; want none", early, err)
	}
	later, err := s.ClaimDueFlagNotifications(ctx, retry, retry.Add(time.Minute), 10)
	if err != nil || len(later) != 1 || later[0].Attempts != 2 || later[0].LastError != "slack 500" {
		t.Fatalf("claim at retry = %+v, %v", later, err)
	}
	if err := s.SettleFlagNotification(ctx, state.FlagNotificationSettle{ID: later[0].ID, Status: state.FlagNotificationDelivered, Now: retry}); err != nil {
		t.Fatalf("settle delivered: %v", err)
	}
	if swept, err := s.SweepFlagNotifications(ctx, retry, 10); err != nil || swept != 0 {
		t.Fatalf("sweep before cutoff = %d, %v; want 0", swept, err)
	}
	if swept, err := s.SweepFlagNotifications(ctx, retry.Add(time.Second), 10); err != nil || swept != 1 {
		t.Fatalf("sweep = %d, %v; want 1", swept, err)
	}

	chans, err := s.FlagNotificationChannels(ctx, acct, []string{ch.ID})
	if err != nil || len(chans) != 1 || chans[0].ID != ch.ID {
		t.Fatalf("channels = %+v, %v", chans, err)
	}
	if chans, err := s.FlagNotificationChannels(ctx, "00000000-0000-4000-8000-000000000001", []string{ch.ID}); err != nil || len(chans) != 0 {
		t.Fatalf("foreign account channels = %+v, %v; want none", chans, err)
	}
}
