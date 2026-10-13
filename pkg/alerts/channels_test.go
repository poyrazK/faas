package alerts_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type sentNotification struct {
	target alertchannels.Target
	msg    alertchannels.Message
}

type recordingChannels struct {
	mu   sync.Mutex
	sent []sentNotification
	fail map[string]error // by kind
}

func (r *recordingChannels) Send(_ context.Context, t alertchannels.Target, msg alertchannels.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentNotification{t, msg})
	return r.fail[t.Kind]
}

// seedChannelRule replaces seedRule's rule with one that has no webhook
// and delivers to a Slack and an email channel.
func seedChannelRule(t *testing.T, store *state.MemStore) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte("https://hooks.slack.com/services/T1/B2/secret"), 512)
	if err != nil {
		t.Fatal(err)
	}
	slack, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: base.AccountID, Name: "ops", Kind: "slack", TargetSealed: sealed}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	email, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: base.AccountID, Name: "me", Kind: "email", Email: "me@example.com"}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.Name, base.WebhookURL, base.WebhookSecretSealed = "", base.Name+"-channels", "", []byte{}
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlertRuleChannels(ctx, rule.ID, []string{slack.ID, email.ID}); err != nil {
		t.Fatal(err)
	}
	return rule, ident
}

func TestEvaluator_ChannelOnlyRuleFiresAndResolves(t *testing.T) {
	store := state.NewMemStore()
	rule, ident := seedChannelRule(t, store)
	channels := &recordingChannels{fail: map[string]error{"email": errors.New("mailbox full")}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	value := 12.0
	var outcomesMu sync.Mutex
	var outcomes []string
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     &selectivePromQL{fn: func(string) (float64, error) { return value, nil }},
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: dispatch,
		Channels:   channels,
		ChannelOutcome: func(kind, outcome string) {
			outcomesMu.Lock()
			defer outcomesMu.Unlock()
			outcomes = append(outcomes, kind+"="+outcome)
		},
		DashboardBaseURL: "https://gregale.dev",
		Now:              func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) },
		Log:              discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 1 || stats.Delivered != 1 || dispatch.callCount() != 0 {
		t.Fatalf("stats %+v, webhook calls %d; want one fire delivered via channels and no webhook", stats, dispatch.callCount())
	}
	if len(channels.sent) != 2 {
		t.Fatalf("sent %d notifications, want 2", len(channels.sent))
	}
	for _, s := range channels.sent {
		if s.msg.Event != alertchannels.EventFire || s.msg.DedupKey != rule.ID || s.msg.AppSlug != "alert-app" || s.msg.DashboardURL != "https://gregale.dev/dashboard/apps/alert-app" {
			t.Fatalf("message = %+v", s.msg)
		}
		if s.target.Kind == "slack" && s.target.SlackURL != "https://hooks.slack.com/services/T1/B2/secret" {
			t.Fatalf("slack destination not unsealed: %+v", s.target)
		}
	}
	chans, _ := store.ListNotificationChannels(context.Background(), rule.AccountID)
	for _, c := range chans {
		if c.Kind == "email" && c.LastError != "mailbox full" || c.Kind == "slack" && c.LastDeliveredAt.IsZero() {
			t.Fatalf("channel status not recorded: %+v", c)
		}
	}

	// Recovery: the next healthy tick sends a resolve to every channel.
	value = 1
	channels.sent = nil
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(channels.sent) != 2 || channels.sent[0].msg.Event != alertchannels.EventResolve {
		t.Fatalf("resolve notifications = %+v", channels.sent)
	}
	if len(outcomes) != 4 {
		t.Fatalf("outcomes = %v", outcomes)
	}
}

func TestEvaluator_ChannelOnlyRuleFailsWhenNoChannelAccepts(t *testing.T) {
	store := state.NewMemStore()
	_, ident := seedChannelRule(t, store)
	channels := &recordingChannels{fail: map[string]error{"email": errors.New("down"), "slack": errors.New("down")}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: &selectivePromQL{fn: func(string) (float64, error) { return 50, nil }},
		Audit: audit.New(store, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return ident },
		Dispatcher: &recordingDispatcher{}, Channels: channels, Log: discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil || stats.Fired != 1 || stats.Failed != 1 {
		t.Fatalf("stats %+v err %v; want the fire recorded as failed", stats, err)
	}
}
