package faas

import (
	"context"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// EnvironmentRouteBudgetReceipt describes original policy intent only.
type EnvironmentRouteBudgetReceipt = api.EnvironmentRouteBudgetReceipt

// GetEnvironmentRouteBudgetReceipt reads historical policy intent without
// forwarding an inherited mutation idempotency key. Omit environment for default.
func (c *Client) GetEnvironmentRouteBudgetReceipt(ctx context.Context, slug, environment, requestID string) (EnvironmentRouteBudgetReceipt, error) {
	ctx = context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
	return c.Client.GetEnvironmentRouteBudgetReceipt(ctx, slug, environment, requestID)
}
