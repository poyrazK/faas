package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261122190000: saved insights are unique per environment and bounded,
// due ones are leased once with their plan and environment, a settle after
// an edit leaves the edit's schedule, and channels resolve within the
// account.
func TestPg_SavedInsights(t *testing.T) {
	f := newFlagExperimentPgFixture(t, "saved-insights")
	s, ctx, acct := f.s, f.ctx, f.acct
	now := time.Now().UTC().Truncate(time.Second)
	ch, err := s.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct, Name: "ops", Kind: "slack",
		TargetSealed: []byte("sealed"), TargetHint: "hooks.slack.com/…/abcd"}, 10)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	due := now.Add(-time.Minute)
	in := state.SavedInsight{AccountID: acct, EnvironmentID: f.env, Key: "orders", Name: "Orders", Kind: "events",
		Query: json.RawMessage(`{"event":"order"}`), RangeDays: 7, Digest: "daily", AlertComparison: "lt", AlertThreshold: 10,
		ChannelIDs: []string{ch.ID}, NextDigestAt: &due, NextCheckAt: &due, CreatedBy: acct}
	si, err := s.CreateSavedInsight(ctx, in, 1)
	if err != nil || si.AlertState != state.SavedInsightOK || len(si.ChannelIDs) != 1 || si.ChannelIDs[0] != ch.ID {
		t.Fatalf("CreateSavedInsight = %+v, %v", si, err)
	}
	if _, err := s.CreateSavedInsight(ctx, in, 5); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate = %v; want ErrConflict", err)
	}
	other := in
	other.Key = "other"
	if _, err := s.CreateSavedInsight(ctx, other, 1); !errors.Is(err, state.ErrSavedInsightLimit) {
		t.Fatalf("over the bound = %v; want ErrSavedInsightLimit", err)
	}

	claimed, err := s.ClaimDueSavedInsights(ctx, now, now.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].Plan == "" || claimed[0].EnvironmentSlug != "production" || claimed[0].ProjectID != f.project {
		t.Fatalf("claim = %+v, %v; want the saved insight with plan and environment", claimed, err)
	}
	if again, err := s.ClaimDueSavedInsights(ctx, now, now.Add(time.Minute), 10); err != nil || len(again) != 0 {
		t.Fatalf("second claim = %+v, %v; want none while leased", again, err)
	}
	chans, err := s.SavedInsightChannels(ctx, acct, claimed[0].ChannelIDs)
	if err != nil || len(chans) != 1 || chans[0].ID != ch.ID {
		t.Fatalf("channels = %+v, %v", chans, err)
	}
	if chans, err := s.SavedInsightChannels(ctx, "00000000-0000-4000-8000-000000000001", claimed[0].ChannelIDs); err != nil || len(chans) != 0 {
		t.Fatalf("foreign account channels = %+v, %v; want none", chans, err)
	}

	value, next := 4.0, now.Add(15*time.Minute)
	if applied, err := s.SettleSavedInsight(ctx, state.SavedInsightSettle{ID: si.ID, ClaimedUpdatedAt: claimed[0].UpdatedAt, Now: now,
		LastValue: &value, AlertState: state.SavedInsightFiring, NextDigestAt: &next, NextCheckAt: &next}); err != nil || !applied {
		t.Fatalf("settle = %v, %v; want applied", applied, err)
	}
	got, err := s.GetSavedInsight(ctx, f.env, "orders")
	if err != nil || got.AlertState != state.SavedInsightFiring || got.LastValue == nil || *got.LastValue != 4 || got.NextCheckAt == nil || !got.NextCheckAt.Equal(next) {
		t.Fatalf("after settle = %+v, %v", got, err)
	}

	// An edit during a run wins over the run's settle; a changed alert resets the state.
	claimed, err = s.ClaimDueSavedInsights(ctx, next, next.Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	edit := in
	edit.AlertThreshold, edit.Digest, edit.NextDigestAt = 20, "", nil
	editNext := next.Add(time.Hour)
	edit.NextCheckAt = &editNext
	time.Sleep(10 * time.Millisecond)
	if updated, err := s.UpdateSavedInsight(ctx, edit); err != nil || updated.AlertState != state.SavedInsightOK || updated.AlertThreshold != 20 {
		t.Fatalf("UpdateSavedInsight = %+v, %v", updated, err)
	}
	if applied, err := s.SettleSavedInsight(ctx, state.SavedInsightSettle{ID: si.ID, ClaimedUpdatedAt: claimed[0].UpdatedAt, Now: next,
		AlertState: state.SavedInsightFiring, NextCheckAt: &next}); err != nil || applied {
		t.Fatalf("stale settle = %v, %v; want not applied", applied, err)
	}
	got, _ = s.GetSavedInsight(ctx, f.env, "orders")
	if got.AlertState != state.SavedInsightOK || got.NextCheckAt == nil || !got.NextCheckAt.Equal(editNext) {
		t.Fatalf("stale settle overwrote the edit: %+v", got)
	}
	if again, err := s.ClaimDueSavedInsights(ctx, editNext, editNext.Add(time.Minute), 10); err != nil || len(again) != 1 {
		t.Fatalf("claim after stale settle = %+v, %v; want the lease released", again, err)
	}

	// A saved insight that sends nothing has no check time.
	quiet := in
	quiet.Digest, quiet.AlertComparison, quiet.ChannelIDs, quiet.NextDigestAt, quiet.NextCheckAt = "", "", nil, nil, nil
	if _, err := s.UpdateSavedInsight(ctx, quiet); err != nil {
		t.Fatalf("quiet update: %v", err)
	}
	list, err := s.ListSavedInsights(ctx, f.env)
	if err != nil || len(list) != 1 || list[0].NextCheckAt != nil {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if ok, err := s.DeleteSavedInsight(ctx, f.env, "orders"); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if _, err := s.GetSavedInsight(ctx, f.env, "orders"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("get after delete = %v; want ErrNotFound", err)
	}
}
