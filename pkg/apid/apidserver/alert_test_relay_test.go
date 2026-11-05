package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// relayedChannel creates a Slack channel sealed to a key apid does not hold,
// as the alerting domain seals it (ADR 20261022111812), and returns that key.
func relayedChannel(t *testing.T, e apidserver.TestEnv) (state.NotificationChannel, *age.X25519Identity) {
	t.Helper()
	domain := mustSealIdentity(t)
	sealed, err := secretbox.SealBytes(domain.Recipient(), alertchannels.SealNamespace, []byte(testSlackURL), 512)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := (*e.Store()).CreateNotificationChannel(context.Background(), state.NotificationChannel{
		AccountID: (*e.Acct()).ID, Name: "relayed", Kind: "slack", TargetSealed: sealed,
	}, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	prevPoll := *apidserver.TestRelayPoll
	*apidserver.TestRelayPoll = 5 * time.Millisecond
	t.Cleanup(func() { *apidserver.TestRelayPoll = prevPoll })
	return ch, domain
}

// TestNotificationChannelTest_RelaysToMeterd pins section 3 of ADR
// 20261022111812: apid cannot open a domain-sealed destination, so meterd
// sends the test and apid answers with meterd's result.
func TestNotificationChannelTest_RelaysToMeterd(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	ch, domain := relayedChannel(t, e)
	local := &recordingChannelSender{}
	withChannelSender(t, local)

	meterd := &recordingChannelSender{}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:    *e.Store(),
		Identity: func() *age.X25519Identity { return domain },
		Channels: meterd,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_, _ = ev.RunTestSendsOnce(ctx)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	defer func() { cancel(); <-done }()

	rec := e.Do(t, http.MethodPost, "/v1/notification-channels/"+ch.ID+"/test", nil, nil)
	var result api.TestNotificationChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != http.StatusOK || !result.Delivered {
		t.Fatalf("test send = %d %s", rec.Code, rec.Body)
	}
	if len(local.targets) != 0 {
		t.Fatal("apid sent the test itself; it cannot open the destination")
	}
	if len(meterd.targets) != 1 || meterd.targets[0].SlackURL != testSlackURL {
		t.Fatalf("meterd sent %+v; want the opened Slack destination", meterd.targets)
	}
	got, err := (*e.Store()).GetNotificationChannel(context.Background(), ch.AccountID, ch.ID)
	if err != nil || got.LastDeliveredAt.IsZero() {
		t.Fatalf("the relayed test was not recorded on the channel: %+v, %v", got, err)
	}
	if _, open, _ := (*e.Store()).ClaimAlertTestSend(context.Background(), time.Minute); open {
		t.Fatal("the relayed request was left behind")
	}
}

// TestNotificationChannelTest_RelayTimesOut: with no meterd answering, apid
// gives up with a 504 and removes its request.
func TestNotificationChannelTest_RelayTimesOut(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	ch, _ := relayedChannel(t, e)
	prevWait := *apidserver.ChannelTestRelayWait
	*apidserver.ChannelTestRelayWait = 50 * time.Millisecond
	t.Cleanup(func() { *apidserver.ChannelTestRelayWait = prevWait })

	rec := e.Do(t, http.MethodPost, "/v1/notification-channels/"+ch.ID+"/test", nil, nil)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("test send = %d %s; want 504", rec.Code, rec.Body)
	}
	if _, open, _ := (*e.Store()).ClaimAlertTestSend(context.Background(), time.Minute); open {
		t.Fatal("the timed-out request was left behind")
	}
}
