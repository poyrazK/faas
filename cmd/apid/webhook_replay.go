package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
)

func (s *server) claimWebhookDelivery(ctx context.Context, provider, deliveryID string) (bool, error) {
	now := time.Now().UTC()
	return s.store.ClaimWebhookDelivery(ctx, provider, deliveryID,
		now.Add(-webhookdedupe.TTL), now.Add(webhookdedupe.TTL))
}

func (s *server) releaseWebhookDelivery(ctx context.Context, provider, deliveryID string) {
	if deliveryID == "" {
		return
	}
	releaser, ok := any(s.store).(state.WebhookDeliveryReleaser)
	if !ok {
		s.log.Error("webhook replay store cannot release claims", "provider", provider)
		return
	}
	if err := releaser.ReleaseWebhookDelivery(ctx, provider, deliveryID); err != nil {
		s.log.Warn("release webhook replay claim failed", "provider", provider, "err", err)
	}
}
