package meter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (p *Pusher) reserveUsageIntent(ctx context.Context, provider, account string, meter state.BillingMeter, hour time.Time, quantity int64) (int64, error) {
	intents, ok := p.store.(state.BillingUsageIntentStore)
	if !ok {
		return 0, errors.New("meter: immutable billing intent store unavailable")
	}
	q, err := intents.ReserveBillingUsageIntent(ctx, provider, account, meter, hour, quantity)
	if err != nil {
		return 0, fmt.Errorf("meter: reserve billing intent: %w", err)
	}
	return q, nil
}
