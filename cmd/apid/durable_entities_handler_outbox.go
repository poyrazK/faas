// adr: 844
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// This callback boundary runs before the engine uploads or publishes a state
// candidate. Bucket CAS still fences ownership; admission is checked again at
// relay acceptance, since SQL registration cannot share a bucket transaction.
func (s *server) durableEntityHandlerTransition(ctx context.Context, id durableentity.ID, body []byte, protocol int) (durableentity.Transition, error) {
	transition, err := durableentity.DecodeTransitionForProtocol(body, protocol)
	if err != nil || len(transition.Outbox) == 0 {
		return transition, err
	}
	if !s.durableEntityOutboxHandlersEnabled || !s.durableEntityOutboxEnabled {
		return durableentity.Transition{}, durableentity.ErrInvalid
	}
	if _, _, err := s.durableEntityOutboxAdmission(ctx, id); err != nil {
		return durableentity.Transition{}, err
	}
	seen := map[string]bool{}
	for _, intent := range transition.Outbox {
		if seen[intent.WebhookID] {
			continue
		}
		if err := s.durableEntityHandlerDestination(ctx, id, intent.WebhookID); err != nil {
			return durableentity.Transition{}, err
		}
		seen[intent.WebhookID] = true
	}
	return transition, nil
}

func (s *server) durableEntityHandlerDestination(ctx context.Context, id durableentity.ID, webhookID string) error {
	hook, err := s.store.AppWebhookByID(ctx, webhookID)
	if errors.Is(err, state.ErrNotFound) {
		return durableentity.ErrInvalid
	}
	if err != nil {
		return api.ErrCapacity("check entity outgoing destination")
	}
	if hook.Scope != state.AppWebhookScopeApp || hook.AccountID != id.AccountID || hook.AppID != id.AppID || !hook.Enabled {
		return durableentity.ErrInvalid
	}
	return nil
}
