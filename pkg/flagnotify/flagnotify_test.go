package flagnotify_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flagnotify"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

var where = flagnotify.Where{AccountID: "acct-1", EnvironmentID: "env-1", OrgID: "org-1", Project: "shop", Environment: "production"}

// ADR-20261125085913 §2: decisions and problems notify, other
// recommendations do not; one key per experiment, action and variant.
func TestExperimentNotification(t *testing.T) {
	tests := []struct {
		action, variant string
		kind            string
		subject         string
	}{
		{flags.RecommendShipVariant, "b", state.FlagNotifyExperimentDecision, "Experiment checkout: ship b"},
		{flags.RecommendKeepControl, "", state.FlagNotifyExperimentDecision, "Experiment checkout: keep control"},
		{flags.RecommendRolledBack, "", state.FlagNotifyExperimentProblem, "Experiment checkout: rolled back by a guardrail"},
		{flags.RecommendFixAllocation, "", state.FlagNotifyExperimentProblem, "Experiment checkout: sample ratio mismatch"},
		{flags.RecommendKeepRunning, "", "", ""},
		{flags.RecommendDecide, "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			in := flagnotify.ExperimentInput{Where: where, Experiment: "checkout", Flag: "checkout-flow", Action: tc.action,
				Variant: tc.variant, Summary: "b is better", Commands: []string{"gregale flag experiment promote checkout b"}}
			n, ok := flagnotify.Experiment(in, []string{"ch-1"})
			if ok != (tc.kind != "") {
				t.Fatalf("notifies = %v; want %v", ok, tc.kind != "")
			}
			if !ok {
				return
			}
			if n.Kind != tc.kind || n.Subject != tc.subject || n.Key != "experiment:env-1:checkout:"+tc.action+":"+tc.variant ||
				!strings.Contains(n.Summary, "Next: gregale flag experiment promote checkout b") ||
				!strings.Contains(n.Summary, "--project shop --environment production") || n.OrgID != "org-1" ||
				!strings.Contains(n.Summary, "Change where these go: gregale flag notify set --channel ID --project shop --environment production") {
				t.Fatalf("notification = %+v", n)
			}
		})
	}
	in := flagnotify.ExperimentInput{Where: where, Experiment: "checkout", Action: flags.RecommendKeepControl, OwnChannels: true}
	if n, _ := flagnotify.Experiment(in, []string{"ch-1"}); !strings.Contains(n.Summary, "sends to the channels it was created with") ||
		strings.Contains(n.Summary, "flag notify set") {
		t.Fatalf("own-channel summary = %q", n.Summary)
	}
	if _, ok := flagnotify.Experiment(flagnotify.ExperimentInput{Where: where, Action: flags.RecommendShipVariant}, nil); ok {
		t.Fatal("a notification without channels was queued")
	}
}

// An experiment's own channels that still exist win; with none left the
// environment's are used.
func TestChannelsPreferTheExperiments(t *testing.T) {
	if got, own := flagnotify.Channels([]string{"own"}, []string{"env"}); len(got) != 1 || got[0] != "own" || !own {
		t.Fatalf("own channels = %v, %v", got, own)
	}
	if got, own := flagnotify.Channels(nil, []string{"env"}); len(got) != 1 || got[0] != "env" || own {
		t.Fatalf("fallback = %v, %v", got, own)
	}
}

// §3: one digest per ISO week listing flags that need attention with a
// command each; one expiry message per flag and expiry date.
func TestLifecycleNotifications(t *testing.T) {
	report := []flags.FlagLifecycle{
		{Key: "old-export", Owner: "payments", ExpiresAt: "2026-11-20", Attention: []string{flags.AttentionExpired}},
		{Key: "orphan", Attention: []string{flags.AttentionNoOwner}},
		{Key: "fine", Owner: "web"},
	}
	monday := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	d, ok := flagnotify.Digest(where, report, []string{"ch-1"}, monday)
	if !ok || d.Kind != state.FlagNotifyLifecycleDigest || d.Key != "lifecycle-digest:env-1:2026-W48" ||
		!strings.Contains(d.Summary, "- old-export (expired; payments): gregale flag delete old-export") ||
		!strings.Contains(d.Summary, "- orphan (no_owner; no owner): gregale flag set orphan --owner TEAM") ||
		strings.Contains(d.Summary, "fine") {
		t.Fatalf("digest = %+v, %v", d, ok)
	}
	if again, _ := flagnotify.Digest(where, report, []string{"ch-1"}, monday.Add(6*24*time.Hour)); again.Key != d.Key {
		t.Fatalf("same week digest key = %s; want %s", again.Key, d.Key)
	}
	if _, ok := flagnotify.Digest(where, report[2:], []string{"ch-1"}, monday); ok {
		t.Fatal("a digest without attention was queued")
	}
	expired := flagnotify.Expired(where, report, []string{"ch-1"})
	if len(expired) != 1 || expired[0].Key != "flag-expired:env-1:old-export:2026-11-20" || expired[0].Kind != state.FlagNotifyFlagExpired ||
		!strings.Contains(expired[0].Summary, "gregale flag delete old-export --project shop --environment production") {
		t.Fatalf("expired = %+v", expired)
	}
}

type fakeSender struct {
	mu      sync.Mutex
	sent    []alertchannels.Message
	to      [][]string
	failing map[string]bool
	// tick runs after each send (advances a test clock).
	tick func()
}

func (f *fakeSender) SendToChannelsFailed(_ context.Context, chans []state.NotificationChannel, msg alertchannels.Message, _ time.Time) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	var to, failed []string
	for _, c := range chans {
		to = append(to, c.ID)
		if f.failing[c.ID] {
			failed = append(failed, c.ID)
		}
	}
	f.to = append(f.to, to)
	if f.tick != nil {
		f.tick()
	}
	return failed
}

func notificationByKey(store *state.MemStore, key string) state.FlagNotification {
	for _, n := range store.FlagNotificationsForTest() {
		if n.Key == key {
			return n
		}
	}
	return state.FlagNotification{}
}

// §4: meterd delivers once, retries with backoff, gives up after the
// attempt limit, skips accounts without the feature and drops
// notifications whose channels are gone.
func TestRunner(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "notify@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "ops", Kind: "slack", TargetSealed: []byte("x")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	queue := func(key, kind string, channels ...string) {
		t.Helper()
		if ok, err := store.EnqueueFlagNotification(ctx, state.FlagNotification{AccountID: acct.ID, EnvironmentID: "env-1", Kind: kind,
			Key: key, Subject: "s " + key, Summary: "body", ChannelIDs: channels}, now); err != nil || !ok {
			t.Fatalf("enqueue %s = %v, %v", key, ok, err)
		}
	}
	queue("ok", state.FlagNotifyExperimentProblem, ch.ID)
	queue("gone", state.FlagNotifyLifecycleDigest, "00000000-0000-4000-8000-000000000009")
	sender := &fakeSender{}
	clock := now
	r := &flagnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock }}
	if n, err := r.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	if len(sender.sent) != 1 || sender.sent[0].Event != alertchannels.EventFire || sender.sent[0].RuleName != "s ok" {
		t.Fatalf("sent = %+v", sender.sent)
	}
	byKey := map[string]state.FlagNotification{}
	for _, n := range store.FlagNotificationsForTest() {
		byKey[n.Key] = n
	}
	if byKey["ok"].Status != state.FlagNotificationDelivered || byKey["gone"].Status != state.FlagNotificationAbandoned {
		t.Fatalf("statuses = %s, %s", byKey["ok"].Status, byKey["gone"].Status)
	}

	queue("failing", state.FlagNotifyExperimentDecision, ch.ID)
	sender.failing = map[string]bool{ch.ID: true}
	for attempt := 1; attempt <= api.FlagsNotificationMaxAttempts; attempt++ {
		if _, err := r.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(time.Hour)
	}
	for _, n := range store.FlagNotificationsForTest() {
		if n.Key == "failing" && (n.Status != state.FlagNotificationAbandoned || n.Attempts != api.FlagsNotificationMaxAttempts) {
			t.Fatalf("failing = %+v; want abandoned after %d attempts", n, api.FlagsNotificationMaxAttempts)
		}
	}

	queue("dark", state.FlagNotifyExperimentDecision, ch.ID)
	r.Released = func(context.Context, state.FlagNotification) bool { return false }
	sent := len(sender.sent)
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range store.FlagNotificationsForTest() {
		if n.Key == "dark" && (n.Status != state.FlagNotificationPending || !n.NextAttemptAt.Equal(clock.Add(flagnotify.NotReleasedRetry))) {
			t.Fatalf("dark = %+v; want pending for an hour", n)
		}
	}
	if len(sender.sent) != sent {
		t.Fatal("a notification of an account without the feature was sent")
	}
}

// Passes while the account waits for the release do not count as delivery
// attempts, so the first real failure afterwards still retries.
func TestRunnerNotReleasedKeepsAttempts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "dark@example.com", api.PlanPro)
	ch, _ := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "ops", Kind: "slack", TargetSealed: []byte("x")}, 10)
	clock := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	if _, err := store.EnqueueFlagNotification(ctx, state.FlagNotification{AccountID: acct.ID, EnvironmentID: "env-1",
		Kind: state.FlagNotifyExperimentDecision, Key: "k", Subject: "s", Summary: "b", ChannelIDs: []string{ch.ID}}, clock); err != nil {
		t.Fatal(err)
	}
	released := false
	sender := &fakeSender{failing: map[string]bool{ch.ID: true}}
	r := &flagnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock },
		Released: func(context.Context, state.FlagNotification) bool { return released }}
	for range api.FlagsNotificationMaxAttempts + 2 {
		if _, err := r.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(flagnotify.NotReleasedRetry)
	}
	if n := notificationByKey(store, "k"); n.Attempts != 0 || n.Status != state.FlagNotificationPending {
		t.Fatalf("after dark passes = %+v; want pending with 0 attempts", n)
	}
	released = true
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := notificationByKey(store, "k"); n.Status != state.FlagNotificationPending || n.Attempts != 1 {
		t.Fatalf("after the first failure = %+v; want a retry", n)
	}
}

// A notification some channels accepted is retried only to the channels
// that did not.
func TestRunnerRetriesOnlyFailedChannels(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "partial@example.com", api.PlanPro)
	slack, _ := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "slack", Kind: "slack", TargetSealed: []byte("x")}, 10)
	pager, _ := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "pager", Kind: "pagerduty", TargetSealed: []byte("x")}, 10)
	clock := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	if _, err := store.EnqueueFlagNotification(ctx, state.FlagNotification{AccountID: acct.ID, EnvironmentID: "env-1",
		Kind: state.FlagNotifyExperimentProblem, Key: "k", Subject: "s", Summary: "b", ChannelIDs: []string{slack.ID, pager.ID}}, clock); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{failing: map[string]bool{pager.ID: true}}
	r := &flagnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock }}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := notificationByKey(store, "k"); n.Status != state.FlagNotificationPending || len(n.ChannelIDs) != 1 || n.ChannelIDs[0] != pager.ID {
		t.Fatalf("after a partial send = %+v; want pending for the pager only", n)
	}
	sender.failing = nil
	clock = clock.Add(time.Hour)
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if last := sender.to[len(sender.to)-1]; len(last) != 1 || last[0] != pager.ID {
		t.Fatalf("retry went to %v; want the pager only", last)
	}
	if n := notificationByKey(store, "k"); n.Status != state.FlagNotificationDelivered {
		t.Fatalf("after the retry = %+v", n)
	}
}

// A batch starts no send once its lease is nearly over: the rest go back
// at once, with no attempt counted.
func TestRunnerHandsBackPastTheLease(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "slow@example.com", api.PlanPro)
	ch, _ := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "ops", Kind: "slack", TargetSealed: []byte("x")}, 10)
	clock := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	for _, key := range []string{"a", "b", "c"} {
		if _, err := store.EnqueueFlagNotification(ctx, state.FlagNotification{AccountID: acct.ID, EnvironmentID: "env-1",
			Kind: state.FlagNotifyExperimentDecision, Key: key, Subject: "s", Summary: "b", ChannelIDs: []string{ch.ID}}, clock); err != nil {
			t.Fatal(err)
		}
	}
	// Each send takes most of the lease.
	sender := &fakeSender{tick: func() { clock = clock.Add(flagnotify.Lease - flagnotify.SendBudget + time.Second) }}
	r := &flagnotify.Runner{Store: store, Channels: sender, Now: func() time.Time { return clock }}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d in one lease; want 1", len(sender.sent))
	}
	pending := 0
	for _, n := range store.FlagNotificationsForTest() {
		if n.Status == state.FlagNotificationPending {
			pending++
			if n.Attempts != 0 || n.NextAttemptAt.After(clock) {
				t.Fatalf("handed back = %+v; want due now with 0 attempts", n)
			}
		}
	}
	if pending != 2 {
		t.Fatalf("pending = %d; want 2", pending)
	}
}

func TestBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 4: 8 * time.Minute, 10: time.Hour} {
		if got := flagnotify.Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %s; want %s", attempts, got, want)
		}
	}
}
