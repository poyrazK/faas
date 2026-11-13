package api

// adr: 20261112200000 — personal-app pull leases. The Go SDK exposed the
// lease verbs only on organization paths, so a Go worker of a personal app
// could not consume a pull binding.

import (
	"context"
	"net/http"
	"net/url"
)

func queueLeasePath(slug, queue, verb string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/queues/" + url.PathEscape(queue) + "/leases" + verb
}

// LeaseQueueMessages leases ready messages from the app's pull binding for
// queue (ADR-1140). WaitSeconds holds the call until a message arrives.
func (c *Client) LeaseQueueMessages(ctx context.Context, slug, queue string, req QueueLeaseRequest) (QueueLeaseResponse, error) {
	var out QueueLeaseResponse
	err := c.do(ctx, http.MethodPost, queueLeasePath(slug, queue, ""), req, &out)
	return out, err
}

// AckQueueLeases completes leased messages.
func (c *Client) AckQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, queueLeasePath(slug, queue, ":ack"), req, &out)
	return out, err
}

// NackQueueLeases returns leased messages for a retry under their policy.
func (c *Client) NackQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, queueLeasePath(slug, queue, ":nack"), req, &out)
	return out, err
}

// ExtendQueueLeases moves the visibility deadline of leased messages.
func (c *Client) ExtendQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, queueLeasePath(slug, queue, ":extend"), req, &out)
	return out, err
}
