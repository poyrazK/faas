package api

import "context"

func (c *Client) ListQueueBindings(ctx context.Context, slug string) ([]QueueBindingResponse, error) {
	var out []QueueBindingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings", nil, &out)
}

func (c *Client) CreateQueueBinding(ctx context.Context, slug string, req CreateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+slug+"/queue-bindings", req, &out, "")
}

func (c *Client) GetQueueBinding(ctx context.Context, slug, id string) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/queue-bindings/"+id, nil, &out)
}

func (c *Client) UpdateQueueBinding(ctx context.Context, slug, id string, req UpdateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/queue-bindings/"+id, req, &out)
}

func (c *Client) DeleteQueueBinding(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/queue-bindings/"+id, nil, nil)
}
