package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// Keep provider/device request JSON opaque: it contains secrets and is never
// returned by a successful call or interpolated into a URL.
type ManagedRealtimePushProviderRequest struct {
	Config  json.RawMessage `json:"config"`
	Enabled bool            `json:"enabled"`
}
type ManagedRealtimePushProviderResponse struct {
	Provider  string    `json:"provider"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}
type ManagedRealtimePushDeviceRequest struct {
	Provider string          `json:"provider"`
	Target   json.RawMessage `json:"target"`
}
type ManagedRealtimePushDeviceResponse struct {
	Device    string    `json:"device"`
	Provider  string    `json:"provider"`
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
type ManagedRealtimePushDeliveryResponse struct {
	NotBefore   time.Time `json:"not_before"`
	CollapseKey string    `json:"collapse_key,omitempty"`
	Priority    string    `json:"priority"`
	GroupKey    string    `json:"group_key,omitempty"`
	GroupLabel  string    `json:"group_label,omitempty"`
	DigestID    string    `json:"digest_id,omitempty"`
	DigestCount int       `json:"digest_count,omitempty"`
	Category    string    `json:"category"`
	ExpiresAt   time.Time `json:"expires_at"`
	ID          string    `json:"id"`
	Device      string    `json:"device"`
	Provider    string    `json:"provider"`
	MessageID   string    `json:"message_id"`
	Sequence    int64     `json:"sequence"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	StatusCode  int       `json:"status_code"`
	Code        string    `json:"code"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	NextAttempt time.Time `json:"next_attempt"`
}

func pushAPIPath(slug, ep string) string {
	return fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/push", url.PathEscape(slug), url.PathEscape(ep))
}
func (c *Client) PutManagedRealtimePushProvider(ctx context.Context, slug, ep, provider string, req ManagedRealtimePushProviderRequest) error {
	var response any
	return c.do(ctx, "PUT", pushAPIPath(slug, ep)+"/providers/"+url.PathEscape(provider), req, &response)
}
func (c *Client) ListManagedRealtimePushProviders(ctx context.Context, slug, ep string) ([]ManagedRealtimePushProviderResponse, error) {
	var out []ManagedRealtimePushProviderResponse
	err := c.do(ctx, "GET", pushAPIPath(slug, ep)+"/providers", nil, &out)
	return out, err
}
func (c *Client) PutManagedRealtimePushDevice(ctx context.Context, slug, ep, principal, device string, req ManagedRealtimePushDeviceRequest) error {
	var response any
	return c.do(ctx, "PUT", pushAPIPath(slug, ep)+"/devices/"+url.PathEscape(device)+"?principal="+url.QueryEscape(principal), req, &response)
}
func (c *Client) DeleteManagedRealtimePushDevice(ctx context.Context, slug, ep, principal, device string) error {
	var response any
	return c.do(ctx, "DELETE", pushAPIPath(slug, ep)+"/devices/"+url.PathEscape(device)+"?principal="+url.QueryEscape(principal), nil, &response)
}
func (c *Client) ListManagedRealtimePushDevices(ctx context.Context, slug, ep, principal string) ([]ManagedRealtimePushDeviceResponse, error) {
	var out []ManagedRealtimePushDeviceResponse
	err := c.do(ctx, "GET", pushAPIPath(slug, ep)+"/devices?principal="+url.QueryEscape(principal), nil, &out)
	return out, err
}
func (c *Client) ListManagedRealtimePushDeliveries(ctx context.Context, slug, ep, principal string) ([]ManagedRealtimePushDeliveryResponse, error) {
	var out []ManagedRealtimePushDeliveryResponse
	err := c.do(ctx, "GET", pushAPIPath(slug, ep)+"/deliveries?principal="+url.QueryEscape(principal), nil, &out)
	return out, err
}

type ManagedRealtimeNotificationRescheduleRequest struct {
	NotificationNotBefore string `json:"notification_not_before"`
}
type ManagedRealtimeNotificationControlResponse struct {
	Fallbacks  int `json:"fallbacks"`
	Deliveries int `json:"deliveries"`
}

func (c *Client) CancelManagedRealtimeNotification(ctx context.Context, slug, ep, principal, messageID string) (ManagedRealtimeNotificationControlResponse, error) {
	var out ManagedRealtimeNotificationControlResponse
	err := c.do(ctx, "DELETE", pushAPIPath(slug, ep)+"/notifications/"+url.PathEscape(messageID)+"?principal="+url.QueryEscape(principal), nil, &out)
	return out, err
}
func (c *Client) RescheduleManagedRealtimeNotification(ctx context.Context, slug, ep, principal, messageID string, req ManagedRealtimeNotificationRescheduleRequest) (ManagedRealtimeNotificationControlResponse, error) {
	var out ManagedRealtimeNotificationControlResponse
	err := c.do(ctx, "PUT", pushAPIPath(slug, ep)+"/notifications/"+url.PathEscape(messageID)+"?principal="+url.QueryEscape(principal), req, &out)
	return out, err
}

type ManagedRealtimeNotificationTimelineEvent struct {
	ID          int64     `json:"id"`
	Principal   string    `json:"-"`
	MessageID   string    `json:"message_id"`
	Device      string    `json:"device,omitempty"`
	DeliveryID  string    `json:"delivery_id,omitempty"`
	Event       string    `json:"event"`
	Reason      string    `json:"reason,omitempty"`
	Attempts    int       `json:"attempts"`
	StatusCode  int       `json:"status_code"`
	OccurredAt  time.Time `json:"occurred_at"`
	NotBefore   time.Time `json:"not_before"`
	NextAttempt time.Time `json:"next_attempt"`
}

func (c *Client) ListManagedRealtimeNotificationTimeline(ctx context.Context, slug, ep, principal, messageID string, before int64) ([]ManagedRealtimeNotificationTimelineEvent, error) {
	var out []ManagedRealtimeNotificationTimelineEvent
	err := c.do(ctx, "GET", pushAPIPath(slug, ep)+"/notifications/"+url.PathEscape(messageID)+"/timeline?principal="+url.QueryEscape(principal)+fmt.Sprintf("&before=%d", before), nil, &out)
	return out, err
}

func snapshotAPIPath(slug, ep, channel string) string {
	return fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/snapshot", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(channel))
}
func (c *Client) GetManagedRealtimeChannelSnapshot(ctx context.Context, slug, ep, channel string) (ManagedRealtimeChannelSnapshotResponse, error) {
	var out ManagedRealtimeChannelSnapshotResponse
	err := c.do(ctx, "GET", snapshotAPIPath(slug, ep, channel), nil, &out)
	return out, err
}
func (c *Client) PutManagedRealtimeChannelSnapshot(ctx context.Context, slug, ep, channel string, req ManagedRealtimeChannelSnapshotRequest) (ManagedRealtimeChannelSnapshotResponse, error) {
	var out ManagedRealtimeChannelSnapshotResponse
	err := c.do(ctx, "PUT", snapshotAPIPath(slug, ep, channel), req, &out)
	return out, err
}
func (c *Client) DeleteManagedRealtimeChannelSnapshot(ctx context.Context, slug, ep, channel string) error {
	var out any
	return c.do(ctx, "DELETE", snapshotAPIPath(slug, ep, channel), nil, &out)
}

func (c *Client) PublishManagedRealtimeChannelBatch(ctx context.Context, slug, ep, channel string, req ManagedRealtimeChannelBatchRequest) (ManagedRealtimeChannelBatchResponse, error) {
	var out ManagedRealtimeChannelBatchResponse
	path := fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/publish-batch", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(channel))
	err := c.do(ctx, "POST", path, req, &out)
	return out, err
}

// PublishManagedRealtimeChannelAtSequence conditionally commits a retained event.
func (c *Client) PublishManagedRealtimeChannelAtSequence(ctx context.Context, slug, ep, channel string, req ManagedRealtimeMessageRequest, expected int64, idempotencyKey string) (ManagedRealtimePublishResponse, error) {
	req.ExpectedSequence = &expected
	return c.PublishManagedRealtimeChannelWithDelivery(ctx, slug, ep, channel, req, ManagedRealtimeDeliveryRetained, idempotencyKey)
}
