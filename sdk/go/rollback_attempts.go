package faas

import (
	"context"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

const RollbackAttemptHeader = "Rollback-Attempt-ID"

// ContextWithRollbackAttempt binds a checked request rollback to its retained
// operation UUID. Persist 32 lowercase hexadecimal characters before sending.
func ContextWithRollbackAttempt(ctx context.Context, id string) context.Context {
	return api.ContextWithRollbackAttempt(ctx, id)
}

// GetRollbackOperation never forwards a caller's mutation replay key.
func (c *Client) GetRollbackOperation(ctx context.Context, slug, id string) (RollbackOperation, error) {
	ctx = context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
	return c.Client.GetRollbackOperation(ctx, slug, id)
}
