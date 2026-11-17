package preview

import (
	"context"

	faas "github.com/gregale-dev/gregale-go"
	"github.com/gregale-dev/gregale-go/internal/api"
)

type EnvironmentPromotionReceipt = api.EnvironmentPromotionReceipt

const EnvironmentPromotionAttemptHeader = api.EnvironmentPromotionAttemptHeader
const EnvironmentPromotionHashHeader = api.EnvironmentPromotionHashHeader

// EnvironmentPromotionRequestHash binds the immutable original environment release.
func EnvironmentPromotionRequestHash(project, source, target, promotionHash string) string {
	return api.EnvironmentPromotionRequestHash(project, source, target, promotionHash)
}

// ContextWithEnvironmentPromotionAttempt affects only environment promotion POSTs.
func ContextWithEnvironmentPromotionAttempt(ctx context.Context, attempt, hash string) context.Context {
	return api.ContextWithEnvironmentPromotionAttempt(ctx, attempt, hash)
}

// GetEnvironmentPromotionReceipt reads acceptance independently from restoration.
func (c *Client) GetEnvironmentPromotionReceipt(ctx context.Context, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	ctx = faas.WithoutIdempotencyKey(ctx)
	return c.PreviewClient.GetEnvironmentPromotionReceipt(ctx, project, environment, attempt)
}

// GetOrgEnvironmentPromotionReceipt reads company admission without retry headers.
func (c *Client) GetOrgEnvironmentPromotionReceipt(ctx context.Context, organization, project, environment, attempt string) (EnvironmentPromotionReceipt, error) {
	return c.PreviewClient.GetOrgEnvironmentPromotionReceipt(faas.WithoutIdempotencyKey(ctx), organization, project, environment, attempt)
}

type PromoteProjectEnvironmentRequest = api.PromoteProjectEnvironmentRequest
