package alerts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// TestEvaluator_RunTestSendsOnce pins meterd's half of the test-send relay
// (ADR 20261022111812, section 3): it opens the destination with its own key,
// sends, and reports the result; a destination it cannot open, or a subject
// of another account, is reported without a send.
func TestEvaluator_RunTestSendsOnce(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	var received atomic.Int32
	var lastBody atomic.Value
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	ctx := context.Background()
	store := state.NewMemStore()
	channelRule, ident := seedChannelRule(t, store)
	secret, err := secretbox.SealBytes(ident.Recipient(), alerts.AlertSecretNamespace, []byte("whsec-relay-test"), 256)
	if err != nil {
		t.Fatal(err)
	}
	webhookRule := channelRule
	webhookRule.ID, webhookRule.Name, webhookRule.WebhookURL, webhookRule.WebhookSecretSealed = "", channelRule.Name+"-webhook", receiver.URL, secret
	if webhookRule, err = store.CreateAlertRule(ctx, webhookRule); err != nil {
		t.Fatal(err)
	}
	chans, err := store.ListNotificationChannels(ctx, channelRule.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	var slack state.NotificationChannel
	for _, c := range chans {
		if c.Kind == "slack" {
			slack = c
		}
	}

	channels := &recordingChannels{}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:    store,
		PromQL:   &selectivePromQL{fn: func(string) (float64, error) { return 0, nil }},
		Audit:    audit.New(store, discardLog(), nil, "meterd"),
		Identity: func() *age.X25519Identity { return ident },
		Channels: channels,
		Now:      time.Now,
		Log:      discardLog(),
	})

	evt, _ := json.Marshal(webhookout.Event{ID: "test-delivery", Rule: webhookRule.Name, RuleName: webhookRule.Name, Payload: map[string]any{"test": true}})
	requests := map[string]state.AlertTestSend{
		"channel":       {Kind: state.AlertTestSendChannel, AccountID: slack.AccountID, SubjectID: slack.ID},
		"webhook":       {Kind: state.AlertTestSendRuleWebhook, AccountID: webhookRule.AccountID, SubjectID: webhookRule.ID, Event: evt},
		"other account": {Kind: state.AlertTestSendChannel, AccountID: uuid.NewString(), SubjectID: slack.ID},
	}
	ids := map[string]string{}
	for name, req := range requests {
		req.ID = uuid.NewString()
		if _, err := store.CreateAlertTestSend(ctx, req); err != nil {
			t.Fatal(err)
		}
		ids[name] = req.ID
	}

	sent, err := ev.RunTestSendsOnce(ctx)
	if err != nil || sent != 3 {
		t.Fatalf("RunTestSendsOnce = %d, %v; want 3 handled", sent, err)
	}
	result := func(name string) state.AlertTestSendResult {
		t.Helper()
		row, err := store.GetAlertTestSend(ctx, requests[name].AccountID, ids[name])
		if err != nil || row.State != state.AlertTestSendDone || row.Result == nil {
			t.Fatalf("%s request = %+v, %v; want done", name, row, err)
		}
		return *row.Result
	}
	if r := result("channel"); !r.Delivered || len(channels.sent) != 1 || channels.sent[0].target.SlackURL != "https://hooks.slack.com/services/T1/B2/secret" || channels.sent[0].msg.Event != alertchannels.EventTest {
		t.Fatalf("channel result %+v, sent %+v", r, channels.sent)
	}
	if r := result("webhook"); !r.Delivered || r.StatusCode != http.StatusNoContent || received.Load() != 1 {
		t.Fatalf("webhook result %+v, receiver calls %d", r, received.Load())
	}
	if body, _ := lastBody.Load().(string); !strings.Contains(body, `"test":true`) {
		t.Fatalf("webhook body = %q, want the relayed test event", body)
	}
	if r := result("other account"); r.Delivered || r.Failure != state.AlertTestSendNotFound {
		t.Fatalf("other account result %+v, want not_found", r)
	}

	// A destination meterd's key does not open is reported, not sent.
	stranger := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:    store,
		PromQL:   &selectivePromQL{fn: func(string) (float64, error) { return 0, nil }},
		Audit:    audit.New(store, discardLog(), nil, "meterd"),
		Identity: func() *age.X25519Identity { id, _ := age.GenerateX25519Identity(); return id },
		Channels: channels,
		Now:      time.Now,
		Log:      discardLog(),
	})
	id := uuid.NewString()
	if _, err := store.CreateAlertTestSend(ctx, state.AlertTestSend{ID: id, Kind: state.AlertTestSendChannel, AccountID: slack.AccountID, SubjectID: slack.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.RunTestSendsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := store.GetAlertTestSend(ctx, slack.AccountID, id); row.Result == nil || row.Result.Failure != state.AlertTestSendUnopenable || len(channels.sent) != 1 {
		t.Fatalf("unopenable result = %+v, sends %d", row.Result, len(channels.sent))
	}
}
