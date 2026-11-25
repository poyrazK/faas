package savedinsightnotify_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/savedinsightnotify"
	"github.com/onebox-faas/faas/pkg/state"
)

type sent struct {
	event    string
	channels []string
	summary  string
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sent
}

func (f *fakeSender) SendToChannels(_ context.Context, chans []state.NotificationChannel, msg alertchannels.Message, _ time.Time) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kinds []string
	for _, c := range chans {
		kinds = append(kinds, c.Kind)
	}
	f.sent = append(f.sent, sent{event: msg.Event, channels: kinds, summary: msg.Summary})
	return len(chans), len(chans)
}

func (f *fakeSender) take() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// ADR-20261122190000 §4: an alert fires once on the transition to
// breaching and resolves once on the way back; a digest goes to every
// channel but PagerDuty; an account without saved insights gets nothing.
func TestRunnerAlertsAndDigests(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	env := "11111111-1111-1111-1111-111111111111"
	slack, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "s", Kind: "slack"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	pd, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "p", Kind: "pagerduty"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "c", "c", 1000)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC) // a Monday, digest time
	track := func(n int) {
		events := make([]state.FlagConversionEvent, n)
		for i := range events {
			events[i] = state.FlagConversionEvent{CustomerID: tenant.ID, Name: "order", At: now.Add(-time.Hour)}
		}
		if _, err := store.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: state.FeatureFlagScope{AccountID: acct.ID, ProjectID: "p", EnvironmentID: env},
			Quota: 1_000_000, MaxNames: 100, Now: now, Events: events, Analytics: true, MaxPropertyKeys: 50}); err != nil {
			t.Fatal(err)
		}
	}
	req := api.SavedInsightRequest{Key: "orders", Kind: "events", Events: &api.InsightEventsQuery{Event: "order"}, Digest: "weekly",
		Alert: &api.SavedInsightAlert{Comparison: "gte", Threshold: 3}, ChannelIDs: []string{slack.ID, pd.ID}}
	query, err := insights.NormalizeSaved(&req, 90)
	if err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	if _, err := store.CreateSavedInsight(ctx, state.SavedInsight{AccountID: acct.ID, EnvironmentID: env, Key: req.Key, Name: req.Name,
		Kind: req.Kind, Query: query, RangeDays: req.RangeDays, Digest: req.Digest, AlertComparison: "gte", AlertThreshold: 3,
		ChannelIDs: req.ChannelIDs, NextDigestAt: &due, NextCheckAt: &due, CreatedBy: acct.ID}, 10); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	released := true
	clock := now
	r := &savedinsightnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock },
		Released: func(context.Context, state.SavedInsight) bool { return released }}
	run := func(advance time.Duration) []sent {
		t.Helper()
		clock = clock.Add(advance)
		if _, err := r.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		return sender.take()
	}

	// 1 order: below the threshold; the weekly digest is due.
	track(1)
	got := run(0)
	if len(got) != 1 || got[0].event != alertchannels.EventDigest || strings.Join(got[0].channels, ",") != "slack" ||
		!strings.Contains(got[0].summary, ": 1.") {
		t.Fatalf("first run sent %+v, want one digest to slack only", got)
	}
	// 3 orders: fires once, to both channels.
	track(2)
	if got := run(api.SavedInsightAlertInterval); len(got) != 1 || got[0].event != alertchannels.EventFire || len(got[0].channels) != 2 {
		t.Fatalf("breach sent %+v, want one fire", got)
	}
	if got := run(api.SavedInsightAlertInterval); len(got) != 0 {
		t.Fatalf("still breaching sent %+v, want nothing", got)
	}
	si, err := store.GetSavedInsight(ctx, env, "orders")
	if err != nil || si.AlertState != state.SavedInsightFiring || si.LastValue == nil || *si.LastValue != 3 {
		t.Fatalf("saved insight = %+v, %v", si, err)
	}
	if si.NextDigestAt == nil || !si.NextDigestAt.Equal(time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("next digest = %v, want next Monday 08:00", si.NextDigestAt)
	}
	// Not released: nothing is sent and it waits an hour.
	released = false
	if got := run(api.SavedInsightAlertInterval); len(got) != 0 {
		t.Fatalf("not released sent %+v", got)
	}
	si, _ = store.GetSavedInsight(ctx, env, "orders")
	if !strings.Contains(si.LastError, "not enabled") || si.NextCheckAt == nil || !si.NextCheckAt.Equal(clock.Add(time.Hour)) {
		t.Fatalf("not released: %+v", si)
	}
	// Released again, a day later the orders are outside a 1-day range.
	released = true
	req.RangeDays = 1
	query, _ = insights.NormalizeSaved(&req, 90)
	next := clock
	if _, err := store.UpdateSavedInsight(ctx, state.SavedInsight{EnvironmentID: env, Key: "orders", Name: "orders", Kind: "events",
		Query: query, RangeDays: 1, Digest: "weekly", AlertComparison: "gte", AlertThreshold: 3, ChannelIDs: req.ChannelIDs,
		NextDigestAt: si.NextDigestAt, NextCheckAt: &next}); err != nil {
		t.Fatal(err)
	}
	if got := run(time.Hour); len(got) != 1 || got[0].event != alertchannels.EventFire {
		t.Fatalf("after edit sent %+v, want a fire (the edit reset the state)", got)
	}
	if got := run(24 * time.Hour); len(got) != 1 || got[0].event != alertchannels.EventResolve {
		t.Fatalf("back under sent %+v, want one resolve", got)
	}
}

// flakyStore fails analytics queries on demand and runs a hook before a
// settle, to edit the saved insight mid-run.
type flakyStore struct {
	savedinsightnotify.Store
	fail         bool
	beforeSettle func()
}

func (f *flakyStore) AnalyticsEventSeries(ctx context.Context, q state.AnalyticsSeriesQuery) ([]state.AnalyticsSeriesRow, error) {
	if f.fail {
		return nil, errors.New("analytics store unavailable")
	}
	return f.Store.AnalyticsEventSeries(ctx, q)
}

func (f *flakyStore) SettleSavedInsight(ctx context.Context, s state.SavedInsightSettle) (bool, error) {
	if f.beforeSettle != nil {
		f.beforeSettle()
	}
	return f.Store.SettleSavedInsight(ctx, s)
}

// ADR-20261122190000 §4: a failed run keeps the alert state and retries a
// due digest instead of dropping it, and a run whose saved insight was
// edited meanwhile sends nothing.
func TestRunnerFailuresAndEdits(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	env := "11111111-1111-1111-1111-111111111111"
	slack, err := mem.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "s", Kind: "slack"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := mem.CreatePlatformTenant(ctx, acct.ID, "c", "c", 1000)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	clock := start
	track := func(n int) {
		t.Helper()
		events := make([]state.FlagConversionEvent, n)
		for i := range events {
			events[i] = state.FlagConversionEvent{CustomerID: tenant.ID, Name: "order", At: clock.Add(-time.Hour)}
		}
		if _, err := mem.IngestFlagEvents(ctx, state.FlagEventIngest{Scope: state.FeatureFlagScope{AccountID: acct.ID, ProjectID: "p", EnvironmentID: env},
			Quota: 1_000_000, MaxNames: 100, Now: clock, Events: events, Analytics: true, MaxPropertyKeys: 50}); err != nil {
			t.Fatal(err)
		}
	}
	req := api.SavedInsightRequest{Key: "orders", Kind: "events", RangeDays: 1, Events: &api.InsightEventsQuery{Event: "order"}, Digest: "daily",
		Alert: &api.SavedInsightAlert{Comparison: "gte", Threshold: 3}, ChannelIDs: []string{slack.ID}}
	query, err := insights.NormalizeSaved(&req, 90)
	if err != nil {
		t.Fatal(err)
	}
	due := start.Add(-time.Minute)
	def := state.SavedInsight{AccountID: acct.ID, EnvironmentID: env, Key: req.Key, Name: req.Name, Kind: req.Kind, Query: query,
		RangeDays: 1, Digest: req.Digest, AlertComparison: "gte", AlertThreshold: 3, ChannelIDs: req.ChannelIDs,
		NextDigestAt: &due, NextCheckAt: &due, CreatedBy: acct.ID}
	if _, err := mem.CreateSavedInsight(ctx, def, 10); err != nil {
		t.Fatal(err)
	}
	store := &flakyStore{Store: mem}
	sender := &fakeSender{}
	r := &savedinsightnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock }}
	run := func(advance time.Duration) string {
		t.Helper()
		clock = clock.Add(advance)
		if _, err := r.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range sender.take() {
			out = append(out, s.event)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	get := func() state.SavedInsight {
		t.Helper()
		si, err := mem.GetSavedInsight(ctx, env, "orders")
		if err != nil {
			t.Fatal(err)
		}
		return si
	}

	// The digest's run fails: nothing is sent, the digest stays due and is
	// retried at the alert interval.
	track(3)
	store.fail = true
	if got := run(0); got != "" {
		t.Fatalf("failed run sent %s", got)
	}
	si := get()
	if si.LastError == "" || si.AlertState != state.SavedInsightOK || si.NextDigestAt == nil || !si.NextDigestAt.Equal(due) ||
		si.NextCheckAt == nil || !si.NextCheckAt.Equal(clock.Add(api.SavedInsightAlertInterval)) {
		t.Fatalf("after failure: %+v", si)
	}
	store.fail = false
	if got, want := run(api.SavedInsightAlertInterval), alertchannels.EventDigest+","+alertchannels.EventFire; got != want {
		t.Fatalf("retry sent %s, want %s", got, want)
	}
	// Firing, the next run fails: the state stays firing, so the next good
	// run below the threshold resolves it exactly once.
	store.fail = true
	if got := run(25 * time.Hour); got != "" {
		t.Fatalf("failed run sent %s", got)
	}
	if si := get(); si.AlertState != state.SavedInsightFiring {
		t.Fatalf("failure changed the alert state: %+v", si)
	}
	store.fail = false
	if got, want := run(api.SavedInsightAlertInterval), alertchannels.EventDigest+","+alertchannels.EventResolve; got != want {
		t.Fatalf("recovery sent %s, want %s", got, want)
	}
	// An edit lands during the run: its settle is skipped and nothing is sent.
	track(3)
	store.beforeSettle = func() {
		next := clock
		edit := def
		edit.NextDigestAt, edit.NextCheckAt = get().NextDigestAt, &next
		if _, err := mem.UpdateSavedInsight(ctx, edit); err != nil {
			t.Fatal(err)
		}
	}
	if got := run(api.SavedInsightAlertInterval); got != "" {
		t.Fatalf("run over an edit sent %s", got)
	}
	store.beforeSettle = nil
	if got := run(0); got != alertchannels.EventFire {
		t.Fatalf("run after the edit sent %s, want a fire", got)
	}
}
