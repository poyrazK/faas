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
	result := state.IssueSlackResult{Status: "failed", Error: "token_unavailable", RetryAt: time.Now().Add(time.Minute)}
	if w.OpenToken != nil {
		token, e := w.OpenToken(c.TokenSealed)
		if e == nil {
			if actions, ok := w.Store.(state.IssueSlackInteractionStore); ok {
				c.Interactive, e = actions.PrepareIssueSlackActions(attempt, c)
				if e != nil {
					return w.finishPreflight(ctx, c, e)
				}
			}
			ready, e := w.Store.IssueSlackDeliveryReady(attempt, c)
			if e != nil {
				return w.finishPreflight(ctx, c, e)
			}
			if !ready {
				result = state.IssueSlackResult{Status: "canceled", Error: "destination_unavailable"}
			} else {
				if e = w.Store.BeginIssueSlackAttempt(attempt, c); e != nil {
					return w.finishPreflight(ctx, c, e)
				}
				client := w.Client
				if client == nil {
					client = NewClient(nil)
				}
				sendCtx, cancelSend := context.WithTimeout(ctx, api.IssueSlackAttemptTimeout)
				result = client.Send(sendCtx, c, token, w.DashboardBase)
				cancelSend()
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

func (w Worker) finishPreflight(ctx context.Context, c state.IssueSlackClaim, cause error) (bool, error) {
	receipt, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.IssueSlackAttemptTimeout)
	defer cancel()
	err := w.Store.FinishIssueSlackDelivery(receipt, c, state.IssueSlackResult{Status: "failed", Error: "transport_error", RetryAt: time.Now().Add(time.Minute)})
	if errors.Is(err, state.ErrIssueSlackClaimLost) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return true, cause
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
