package api

import "context"

func (c *PreviewClient) ListQueueBindings(ctx context.Context, slug string) ([]QueueBindingResponse, error) {
	var out []QueueBindingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings", nil, &out)
}

// ListQueueBindingHistory includes retired identities for reviewed recovery.
func (c *PreviewClient) ListQueueBindingHistory(ctx context.Context, slug string) ([]QueueBindingResponse, error) {
	var out []QueueBindingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings?include_retired=true", nil, &out)
}

func (c *PreviewClient) CreateQueueBinding(ctx context.Context, slug string, req CreateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+slug+"/queue-bindings", req, &out, "")
}

func (c *PreviewClient) GetQueueBinding(ctx context.Context, slug, id string) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings/"+id, nil, &out)
}

// GetQueueBindingStatus returns the binding's durable consumer projection and
// lease-aware queue counters. ConsumerState is control-plane state, not a
// broker connection liveness guarantee.
func (c *PreviewClient) GetQueueBindingStatus(ctx context.Context, slug, id string) (QueueBindingStatusResponse, error) {
	var out QueueBindingStatusResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings/"+id+"/status", nil, &out)
}

func (c *PreviewClient) UpdateQueueBinding(ctx context.Context, slug, id string, req UpdateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/queue-bindings/"+id, req, &out)
}

func (c *PreviewClient) DeleteQueueBinding(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/queue-bindings/"+id, nil, nil)
}
