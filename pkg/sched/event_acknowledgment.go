package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (d *Drain) prepareEventAcknowledgment(ctx context.Context, inv state.Invocation) (string, error) {
	seconds, err := state.EventAcknowledgmentSeconds(inv)
	if err != nil || seconds == 0 {
		return "", err
	}
	budget := d.invocationAttemptBudget(ctx, inv)
	if inv.Attempts > budget {
		return "", fmt.Errorf("event acknowledgment attempt budget exhausted")
	}
	store, ok := d.store.(state.EventAcknowledgmentStore)
	if !ok {
		return "", fmt.Errorf("event acknowledgments unavailable")
	}
	delay := d.invocationRetryDelay(inv)
	if delay <= 0 {
		delay = api.EventSubscriptionControlRetryDelay
	}
	return store.OfferEventAcknowledgment(ctx, inv, budget, delay)
}
