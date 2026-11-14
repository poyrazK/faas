package faas

import (
	"context"
	"github.com/gregale-dev/gregale-go/internal/api"
)

const ServiceAbortAttemptHeader = api.ServiceAbortAttemptHeader

type ServiceAbortReceipt = api.ServiceAbortReceipt
type AppReleaseReview = api.AppReleaseReview

// ContextWithServiceAbortAttempt binds an exact reviewed service
// abort to immutable acceptance evidence. Save the opaque ID before submission.
func ContextWithServiceAbortAttempt(ctx context.Context, id string) context.Context {
	return api.ContextWithServiceAbortAttempt(ctx, id)
}

// GetServiceAbortReceipt never forwards a mutation replay key.
func (c *Client) GetServiceAbortReceipt(ctx context.Context, slug, attemptID string) (ServiceAbortReceipt, error) {
	ctx = context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
	return c.Client.GetServiceAbortReceipt(ctx, slug, attemptID)
}

// GetAppReleaseReview reads a fresh guard without forwarding a mutation key.
func (c *Client) GetAppReleaseReview(ctx context.Context, slug string) (AppReleaseReview, error) {
	ctx = context.WithValue(ctx, idempotencyKeyCtxKey{}, IdempotencyKey(""))
	return c.Client.GetAppReleaseReview(ctx, slug)
}
