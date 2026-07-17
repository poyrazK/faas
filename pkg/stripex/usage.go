package stripex

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// pushUsageRecordSDK is the seam where stripe-go lands. M7 wires a real
// subscriptionItem.UsageRecord call here; until then the function is a
// placeholder that returns nil so the dedupe table + pusher loop are
// exercised end-to-end without the SDK.
//
// The placeholder behavior is documented in the runbook so the M7 sign-
// off reviewer knows we're not silently dropping the actual bill —
// production swap is a single function body replacement, plus the
// go.mod entry for stripe-go.
//
// TODO(m7-real-stripe): replace this body with the stripe-go call below:
//
//	idem := acct.ID + ":" + hour.Format(time.RFC3339)
//	sparams := &stripe.UsageRecordParams{
//	    Quantity: stripe.Int64(int64(gbHours * 1000)), // millicents-precision not needed; round to 3 dp
//	    Timestamp: stripe.Int64(hour.Unix()),
//	}
//	sparams.IdempotencyKey = stripe.String(idem)
//	_, err := usagerecord.New(params.SubscriptionItem, sparams)
func (c *Client) pushUsageRecordSDK(_ context.Context, _ state.Account, _ time.Time, _ float64) error {
	return nil
}
