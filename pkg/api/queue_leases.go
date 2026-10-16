package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// Pull leases (ADR-1140) let worker and job apps consume a queue that has an
// enabled pull binding. A lease hides a message for its visibility timeout;
// the consumer then acks, nacks or extends it by receipt. Unacked messages
// are redelivered after the timeout with a new attempt.

// QueueLeaseRequest is the body for POST /v1/apps/{slug}/queues/{queue}/leases.
// Zero values select one message and the default visibility timeout.
// WaitSeconds (at most QueueLeaseMaxWaitSeconds) holds a call that finds no
// ready message until one arrives or the wait ends; zero returns at once.
type QueueLeaseRequest struct {
	MaxMessages              int `json:"max_messages,omitempty"`
	VisibilityTimeoutSeconds int `json:"visibility_timeout_seconds,omitempty"`
	WaitSeconds              int `json:"wait_seconds,omitempty"`
}

// QueueLeaseMessage is one leased message. Receipt is opaque and acts only
// while this lease is current.
type QueueLeaseMessage struct {
	ID             string            `json:"id"`
	Receipt        string            `json:"receipt"`
	Payload        json.RawMessage   `json:"payload"`
	Headers        map[string]string `json:"headers,omitempty"`
	Attempt        int               `json:"attempt"`
	EnqueuedAt     time.Time         `json:"enqueued_at"`
	LeaseExpiresAt time.Time         `json:"lease_expires_at"`
}

// QueueLeaseResponse lists leased messages; it is empty when none are ready.
type QueueLeaseResponse struct {
	Messages []QueueLeaseMessage `json:"messages"`
}

// QueueLeaseReceiptsRequest is the body for the ack, nack and extend calls.
// DelaySeconds (nack) postpones redelivery beyond the binding's retry
// back-off; Error (nack) is recorded as the attempt's last error;
// VisibilityTimeoutSeconds (extend) is the new timeout from now.
type QueueLeaseReceiptsRequest struct {
	Receipts                 []string `json:"receipts"`
	DelaySeconds             int      `json:"delay_seconds,omitempty"`
	Error                    string   `json:"error,omitempty"`
	VisibilityTimeoutSeconds int      `json:"visibility_timeout_seconds,omitempty"`
}

// QueueLeaseReceiptFailure explains why one receipt was not applied.
// Reason is "lease_expired" when the receipt no longer owns its message.
type QueueLeaseReceiptFailure struct {
	Receipt string `json:"receipt"`
	Reason  string `json:"reason"`
}

// QueueLeaseReceiptsResponse reports which receipts were applied.
type QueueLeaseReceiptsResponse struct {
	Succeeded []string                   `json:"succeeded"`
	Failed    []QueueLeaseReceiptFailure `json:"failed"`
}

func queueLeasesPath(slug, queue string) string {
	return "/v1/apps/" + slug + "/queues/" + url.PathEscape(queue) + "/leases"
}

// LeaseQueueMessages leases up to MaxMessages ready messages from a queue with
// an enabled pull binding.
func (c *Client) LeaseQueueMessages(ctx context.Context, slug, queue string, req QueueLeaseRequest) (QueueLeaseResponse, error) {
	var out QueueLeaseResponse
	return out, c.do(ctx, "POST", queueLeasesPath(slug, queue), req, &out)
}

// AckQueueLeases completes leased messages.
func (c *Client) AckQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	return out, c.do(ctx, "POST", queueLeasesPath(slug, queue)+":ack", req, &out)
}

// NackQueueLeases returns leased messages for a retry under the binding's
// retry policy, or dead-letters them once the attempt budget is spent.
func (c *Client) NackQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	return out, c.do(ctx, "POST", queueLeasesPath(slug, queue)+":nack", req, &out)
}

// ExtendQueueLeases moves the visibility deadline of leased messages.
func (c *Client) ExtendQueueLeases(ctx context.Context, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	return out, c.do(ctx, "POST", queueLeasesPath(slug, queue)+":extend", req, &out)
}
