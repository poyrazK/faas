package faas

import (
	"context"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// DeploymentPromotionReceipt proves original acceptance, not current traffic.
type DeploymentPromotionReceipt = api.DeploymentPromotionReceipt

// GetDeploymentPromotionReceipt reads the original receipt without replaying traffic.
func (c *Client) GetDeploymentPromotionReceipt(ctx context.Context, slug, attemptID string) (DeploymentPromotionReceipt, error) {
	ctx = context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
	return c.Client.GetDeploymentPromotionReceipt(ctx, slug, attemptID)
}
