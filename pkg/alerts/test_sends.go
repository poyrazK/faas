package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// Test sends (ADR 20261022111812, section 3). apid relays a test of a
// notification channel or an alert rule's webhook here when it cannot open
// the destination itself; meterd holds the alerting key.

// TestSendLease bounds how long one claimed test send may take: a webhook
// test retries like a real delivery, up to about 80 seconds.
const TestSendLease = 2 * time.Minute

// TestSendRetention is how long an unanswered test send is kept: apid waits
// far less, so older rows were abandoned.
const TestSendRetention = time.Hour

// RunTestSendsOnce claims and sends every waiting test send, and prunes
// abandoned ones at most once a minute. It reports how many it sent. One
// goroutine calls it; several meterds share the queue through the claim.
func (e *Evaluator) RunTestSendsOnce(ctx context.Context) (int, error) {
	store, ok := e.store.(state.AlertTestSendStore)
	if !ok {
		return 0, nil
	}
	if now := e.now(); now.Sub(e.testSendsPrunedAt) > time.Minute {
		if _, err := store.PruneAlertTestSends(ctx, now.Add(-TestSendRetention)); err != nil {
			return 0, err
		}
		e.testSendsPrunedAt = now
	}
	sent := 0
	for {
		req, ok, err := store.ClaimAlertTestSend(ctx, TestSendLease)
		if err != nil || !ok {
			return sent, err
		}
		result := e.sendTest(ctx, req)
		if err := store.CompleteAlertTestSend(ctx, req.ID, req.ClaimToken, result); err != nil && !errors.Is(err, state.ErrConflict) {
			return sent, fmt.Errorf("alerts: complete test send %s: %w", req.ID, err)
		}
		sent++
	}
}

func (e *Evaluator) sendTest(ctx context.Context, req state.AlertTestSend) state.AlertTestSendResult {
	switch req.Kind {
	case state.AlertTestSendChannel:
		return e.sendChannelTest(ctx, req)
	case state.AlertTestSendRuleWebhook:
		return e.sendRuleWebhookTest(ctx, req)
	}
	return state.AlertTestSendResult{Failure: state.AlertTestSendNotFound}
}

func (e *Evaluator) sendChannelTest(ctx context.Context, req state.AlertTestSend) state.AlertTestSendResult {
	channels, ok := e.store.(state.NotificationChannelStore)
	if !ok || e.channels == nil {
		return state.AlertTestSendResult{Failure: state.AlertTestSendUnopenable}
	}
	ch, err := channels.GetNotificationChannel(ctx, req.AccountID, req.SubjectID)
	if err != nil {
		return state.AlertTestSendResult{Failure: state.AlertTestSendNotFound}
	}
	target, ok := openTarget(e.unsealIdentities(), ch)
	if !ok {
		return state.AlertTestSendResult{Failure: state.AlertTestSendUnopenable}
	}
	msg := alertchannels.Message{Event: alertchannels.EventTest, RuleName: "test", DedupKey: "test-" + ch.ID, OccurredAt: e.now()}
	sendCtx, cancel := context.WithTimeout(ctx, channelSendBudget)
	defer cancel()
	if err := e.channels.Send(sendCtx, target, msg); err != nil {
		return state.AlertTestSendResult{Error: err.Error()}
	}
	return state.AlertTestSendResult{Delivered: true}
}

func (e *Evaluator) sendRuleWebhookTest(ctx context.Context, req state.AlertTestSend) state.AlertTestSendResult {
	rule, err := e.store.AlertRuleByID(ctx, req.SubjectID)
	if err != nil || rule.AccountID != req.AccountID || rule.WebhookURL == "" {
		return state.AlertTestSendResult{Failure: state.AlertTestSendNotFound}
	}
	ids := e.unsealIdentities()
	if len(ids) == 0 {
		return state.AlertTestSendResult{Failure: state.AlertTestSendUnopenable}
	}
	_, secret, err := secretbox.OpenBytesMulti(ids, rule.WebhookSecretSealed)
	if err != nil || len(secret) == 0 {
		return state.AlertTestSendResult{Failure: state.AlertTestSendUnopenable}
	}
	defer clear(secret)
	var evt webhookout.Event
	if err := json.Unmarshal(req.Event, &evt); err != nil {
		return state.AlertTestSendResult{Failure: state.AlertTestSendNotFound}
	}
	dispatcher := webhookout.NewDispatcher(webhookout.DispatcherOptions{HeaderSet: webhookout.HeaderSetAlert})
	r := dispatcher.DispatchTest(ctx, webhookout.Target{URL: rule.WebhookURL, Signer: webhookout.NewSigner(secret)}, evt)
	out := state.AlertTestSendResult{Delivered: r.Err == nil, StatusCode: r.StatusCode, Attempts: r.Attempts}
	if r.Err != nil {
		out.Error = r.Err.Error()
	}
	return out
}
