package preview

import (
	"context"

	faas "github.com/gregale-dev/gregale-go"
	"github.com/gregale-dev/gregale-go/internal/api"
)

type EnvironmentRollbackReceipt = api.EnvironmentRollbackReceipt

const EnvironmentRollbackAttemptHeader = api.EnvironmentRollbackAttemptHeader
const EnvironmentRollbackHashHeader = api.EnvironmentRollbackHashHeader

// EnvironmentRollbackRequestHash binds the immutable original environment release.
func EnvironmentRollbackRequestHash(project, source, target, promotion, promotionHash string) string {
	return api.EnvironmentRollbackRequestHash(project, source, target, promotion, promotionHash)
}

// ContextWithEnvironmentRollbackAttempt affects only environment rollback POSTs.
func ContextWithEnvironmentRollbackAttempt(ctx context.Context, attempt, hash string) context.Context {
	return api.ContextWithEnvironmentRollbackAttempt(ctx, attempt, hash)
}

// GetEnvironmentRollbackReceipt reads acceptance independently from restoration.
func (c *Client) GetEnvironmentRollbackReceipt(ctx context.Context, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	ctx = faas.WithoutIdempotencyKey(ctx)
	return c.PreviewClient.GetEnvironmentRollbackReceipt(ctx, project, environment, promotion, attempt)
}

// GetOrgEnvironmentRollbackReceipt reads company admission without retry headers.
func (c *Client) GetOrgEnvironmentRollbackReceipt(ctx context.Context, organization, project, environment, promotion, attempt string) (EnvironmentRollbackReceipt, error) {
	return c.PreviewClient.GetOrgEnvironmentRollbackReceipt(faas.WithoutIdempotencyKey(ctx), organization, project, environment, promotion, attempt)
}
