package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateRuleChannels checks an alert rule's channel_ids (ADR-749): at most
// MaxChannelsPerAlertRule, no duplicates, and every id one of the account's
// channels. It returns the ids in request order.
func (s *server) validateRuleChannels(ctx context.Context, accountID string, ids []string) ([]string, *api.Problem) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > api.MaxChannelsPerAlertRule {
		return nil, api.ErrAlertRuleInvalid(fmt.Sprintf("an alert rule can deliver to at most %d channels", api.MaxChannelsPerAlertRule))
	}
	store, ok := s.store.(state.NotificationChannelStore)
	if !ok {
		return nil, api.ErrCapacity("notification channels are unavailable on this deployment")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, api.ErrAlertRuleInvalid("channel_ids contains " + id + " twice")
		}
		seen[id] = true
		if _, err := store.GetNotificationChannel(ctx, accountID, id); err != nil {
			return nil, api.ErrAlertRuleInvalid("channel_ids: " + id + " is not one of this account's notification channels")
		}
	}
	return ids, nil
}

// sealCreateWebhook guards and seals a create request's webhook. A
// channel-only rule stores no webhook: an empty URL and an empty sealed
// secret, which the evaluator reads as "deliver to channels only".
func sealCreateWebhook(ctx context.Context, req api.CreateAlertRuleRequest) ([]byte, *api.Problem) {
	if req.WebhookURL == "" && req.WebhookSecret == "" {
		return []byte{}, nil
	}
	if prob := resolveAndCheckEgress(ctx, req.WebhookURL); prob != nil {
		return nil, prob
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded — refusing to seal webhook secret")
	}
	sealed, err := secretbox.SealBytes(recipient, alertRuleSecretSealLabel, []byte(req.WebhookSecret), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return nil, prob
		}
		return nil, api.ErrCapacity("could not seal webhook secret")
	}
	return sealed, nil
}

// updatedRuleChannels validates a PATCH's channel change and enforces that
// the rule keeps a destination: a webhook, or at least one channel. It
// returns nil when channels are unchanged.
func (s *server) updatedRuleChannels(ctx context.Context, accountID string, existing, merged state.AlertRule, req *[]string) (*[]string, *api.Problem) {
	count := 0
	if req != nil {
		ids, prob := s.validateRuleChannels(ctx, accountID, *req)
		if prob != nil {
			return nil, prob
		}
		count = len(ids)
	} else if bindings, ok := s.store.(state.AlertRuleChannelStore); ok {
		current, err := bindings.ListAlertRuleChannels(ctx, existing.ID)
		if err != nil {
			return nil, api.ErrCapacity("could not read the rule's notification channels")
		}
		count = len(current)
	}
	if merged.WebhookURL == "" && count == 0 {
		return nil, api.ErrAlertRuleInvalid("an alert rule needs a webhook_url or at least one notification channel")
	}
	return req, nil
}

// bindRuleChannels stores a rule's channels. On a create that fails here
// the caller's rule would have no destination, so the rule is removed.
func (s *server) bindRuleChannels(ctx context.Context, rule state.AlertRule, ids []string) *api.Problem {
	bindings, ok := s.store.(state.AlertRuleChannelStore)
	if !ok {
		if len(ids) == 0 {
			return nil
		}
		return api.ErrCapacity("notification channels are unavailable on this deployment")
	}
	if len(ids) == 0 && rule.WebhookURL != "" {
		// Clearing (or never setting) channels on a webhook rule.
		if err := bindings.SetAlertRuleChannels(ctx, rule.ID, nil); err != nil {
			return api.ErrCapacity("could not update the rule's notification channels")
		}
		return nil
	}
	if err := bindings.SetAlertRuleChannels(ctx, rule.ID, ids); err != nil {
		if rule.WebhookURL == "" {
			_ = s.store.DeleteAlertRule(ctx, rule.ID)
		}
		return api.ErrCapacity("could not bind the rule's notification channels")
	}
	return nil
}

// alertRuleResponseWithChannels adds the rule's channel ids to its response.
func (s *server) alertRuleResponseWithChannels(ctx context.Context, rule state.AlertRule) api.AlertRuleResponse {
	resp := alertRuleResponse(rule)
	if bindings, ok := s.store.(state.AlertRuleChannelStore); ok {
		if chans, err := bindings.ListAlertRuleChannels(ctx, rule.ID); err == nil {
			for _, c := range chans {
				resp.ChannelIDs = append(resp.ChannelIDs, c.ID)
			}
		}
	}
	return resp
}
