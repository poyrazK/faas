package issueslack

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
)

type Worker struct {
	Store         state.IssueSlackStore
	Client        *Client
	OpenToken     func([]byte) (string, error)
	DashboardBase string
}

func (w Worker) Step(ctx context.Context) (bool, error) {
	c, found, err := w.Store.ClaimIssueSlackDelivery(ctx)
	if err != nil || !found || c.Status != "in_flight" {
		return found, err
	}
	attempt, cancel := context.WithTimeout(ctx, api.IssueSlackAttemptTimeout)
	defer cancel()
	result := state.IssueSlackResult{Status: "dead", Error: "token_unavailable"}
	if w.OpenToken != nil {
		token, e := w.OpenToken(c.TokenSealed)
		if e == nil {
			if actions, ok := w.Store.(state.IssueSlackInteractionStore); ok {
				var prepared state.IssueSlackMessageActions
				prepared, e = actions.PrepareIssueSlackActions(attempt, c)
				if e != nil {
					return true, e
				}
				c.Interactive, c.CanAssign = prepared.Enabled, prepared.CanAssign
			}
			ready, e := w.Store.IssueSlackDeliveryReady(attempt, c)
			if e != nil {
				return true, e
			}
			if !ready {
				result = state.IssueSlackResult{Status: "canceled", Error: "destination_unavailable"}
			} else {
				client := w.Client
				if client == nil {
					client = NewClient(nil)
				}
				result = client.Send(attempt, c, token, w.DashboardBase)
			}
		}
	}
	receipt, finish := context.WithTimeout(context.WithoutCancel(ctx), api.IssueSlackAttemptTimeout)
	defer finish()
	err = w.Store.FinishIssueSlackDelivery(receipt, c, result)
	if errors.Is(err, state.ErrIssueSlackClaimLost) {
		err = nil
	}
	return true, err
}
func (w Worker) Batch(ctx context.Context) error {
	for range api.IssueSlackBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := w.Step(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}
func retry(c state.IssueSlackClaim, reason string, code int, after string) state.IssueSlackResult {
	now := time.Now()
	result := state.IssueSlackResult{Status: "dead", Error: reason, ResponseCode: code, RetryAt: slackRetryAfter(after, now)}
	delay, err := webhook.ComputeBackoff(webhook.DefaultBackoffs[state.AppWebhookRetryDefault], c.Attempt-1)
	if err != nil || c.Attempt >= api.IssueSlackMaxAttempts {
		return result
	}
	result.Status = "failed"
	if next := now.Add(delay); next.After(result.RetryAt) {
		result.RetryAt = next
	}
	return result
}
