package alerts_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — meterd delivers release approval events once, to
// Slack and email channels, retrying while every channel fails.
func TestEvaluator_DeliversReleaseApprovalEvents(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "approvals-notify@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(target string) []byte {
		sealed, err := secretbox.SealBytes(ident.Recipient(), alertchannels.SealNamespace, []byte(target), 512)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	channel := func(c state.NotificationChannel) state.NotificationChannel {
		c.AccountID = acct.ID
		created, err := store.CreateNotificationChannel(ctx, c, api.MaxNotificationChannelsPerAccount)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	slack := channel(state.NotificationChannel{Name: "releases", Kind: "slack", TargetSealed: seal("https://hooks.slack.com/services/T1/B2/secret")})
	email := channel(state.NotificationChannel{Name: "leads", Kind: "email", Email: "leads@example.com"})
	pager := channel(state.NotificationChannel{Name: "pager", Kind: "pagerduty", TargetSealed: seal(strings.Repeat("a", 32)), PagerDutyRegion: "us"})

	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	enqueue := func(event string, channelIDs ...string) {
		payload, _ := json.Marshal(alertchannels.ReleaseNotice{Event: "approval_" + event, App: app.Slug, DeploymentID: dep.ID, Approvals: 0, Required: 1,
			ReviewURL: "https://console.gregale.dev/dashboard/releases/" + dep.ID + "/approval"})
		if ok, err := store.EnqueueReleaseApprovalEvent(ctx, state.ReleaseApprovalEvent{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID,
			Event: event, ChannelIDs: channelIDs, Payload: payload}, now); err != nil || !ok {
			t.Fatalf("enqueue %s = %v, %v", event, ok, err)
		}
	}
	enqueue(state.ReleaseApprovalEventRequested, slack.ID, email.ID, pager.ID)
	enqueue(state.ReleaseApprovalEventApproved, email.ID)
	enqueue(state.ReleaseApprovalEventExpired, "6f1b8f1e-9b1c-4f43-8f1a-2d7c3e4b5a60")

	clock := now
	channels := &recordingChannels{fail: map[string]error{"email": errors.New("mailbox full")}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, Identity: func() *age.X25519Identity { return ident }, Channels: channels,
		Now: func() time.Time { return clock }, Log: discardLog(),
	})
	stats, err := ev.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// requested: Slack took it (email failed, PagerDuty is never sent);
	// approved: its only channel failed; expired: its channel is gone.
	if stats.ReleaseApprovalNotices != 1 || len(channels.sent) != 3 {
		t.Fatalf("stats = %+v, sent = %+v", stats, channels.sent)
	}
	for _, s := range channels.sent {
		if s.target.Kind == "pagerduty" || s.msg.Release == nil || s.msg.Release.DeploymentID != dep.ID || s.msg.DedupKey != dep.ID {
			t.Fatalf("sent %+v", s)
		}
		if s.target.Kind == "slack" && s.target.SlackURL != "https://hooks.slack.com/services/T1/B2/secret" {
			t.Fatalf("slack destination not unsealed: %+v", s.target)
		}
	}
	if due, _ := store.DueReleaseApprovalEvents(ctx, now, 10); len(due) != 0 {
		t.Fatalf("due right after the tick = %+v; the failed one waits for its backoff", due)
	}
	due, _ := store.DueReleaseApprovalEvents(ctx, now.Add(api.ReleaseApprovalNotifyRetryBase), 10)
	if len(due) != 1 || due[0].Event != state.ReleaseApprovalEventApproved || due[0].Attempts != 1 || !strings.Contains(due[0].LastError, "leads: mailbox full") {
		t.Fatalf("retry = %+v", due)
	}

	// Every channel keeps failing: the event gives up after the attempt cap.
	for i := 1; i < api.ReleaseApprovalNotifyAttemptsMax; i++ {
		clock = clock.Add(api.ReleaseApprovalNotifyRetryBase << i)
		if _, err := ev.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if due, _ := store.DueReleaseApprovalEvents(ctx, clock.Add(24*time.Hour), 10); len(due) != 0 {
		t.Fatalf("after %d attempts the event must have given up: %+v", api.ReleaseApprovalNotifyAttemptsMax, due)
	}
}
