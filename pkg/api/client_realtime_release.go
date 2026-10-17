// Code generated from the managed realtime OpenAPI surface. DO NOT EDIT.
package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// realtimeReleasePath escapes every path value before expanding the contract template.
func realtimeReleasePath(template string, values map[string]string) string {
	for key, value := range values {
		template = strings.ReplaceAll(template, "{"+key+"}", url.PathEscape(value))
	}
	return template
}

// doRealtimeRelease keeps scoped worker/publisher credentials on their intended origin.
func (c *Client) doRealtimeRelease(ctx context.Context, method, path string, body, out any, headers http.Header) error {
	if !strings.HasPrefix(path, "/v1/realtime/") {
		return c.doWithClientAndHeadersAndIdempotencyKey(ctx, c.http, method, path, body, out, headers.Get("Idempotency-Key"), headers)
	}
	clone := *c.http
	clone.Jar = nil
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c.doWithClientAndHeadersAndIdempotencyKey(ctx, &clone, method, path, body, out, headers.Get("Idempotency-Key"), headers)
}

// streamRealtimeRelease writes bounded-server JSONL exports without JSON decoding or buffering.
func (c *Client) streamRealtimeRelease(ctx context.Context, path string, dst io.Writer, headers http.Header) (int64, error) {
	if dst == nil {
		return 0, fmt.Errorf("export destination is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for key, values := range headers {
		req.Header[key] = values
	}
	clone := *c.http
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := clone.Do(req)
	if err != nil {
		return 0, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, err := readBoundedResponse(resp, maxResponseBodyBytes)
		if err != nil {
			return 0, err
		}
		return 0, apiErrorFromResponse(resp, data)
	}
	return io.Copy(dst, resp.Body)
}

// ListRealtimePublisherCredentials calls GET /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials.
func (c *Client) ListRealtimePublisherCredentials(ctx context.Context, slug string, id string) ([]RealtimePublisherCredential, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	var out []RealtimePublisherCredential
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// IssueRealtimePublisherCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials.
func (c *Client) IssueRealtimePublisherCredential(ctx context.Context, slug string, id string, request RealtimePublisherCredentialRequest) (RealtimePublisherCredentialIssued, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	var out RealtimePublisherCredentialIssued
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// RotateRealtimePublisherCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/rotate.
func (c *Client) RotateRealtimePublisherCredential(ctx context.Context, slug string, id string, credentialID string, request RealtimeWorkerCredentialRotationRequest) (RealtimePublisherCredentialIssued, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/rotate", map[string]string{"slug": slug, "id": id, "credential_id": credentialID})
	headers := http.Header{}
	var out RealtimePublisherCredentialIssued
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// RevokeRealtimePublisherCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/revoke.
func (c *Client) RevokeRealtimePublisherCredential(ctx context.Context, slug string, id string, credentialID string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/revoke", map[string]string{"slug": slug, "id": id, "credential_id": credentialID})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, nil, nil, headers)
}

// PublishWithRealtimePublisherCredential calls POST /v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) PublishWithRealtimePublisherCredential(ctx context.Context, endpointID string, channel string, request RealtimePublisherMessageRequest, options *PublishWithRealtimePublisherCredentialOptions) (RealtimePublisherPublishResponse, error) {
	path := realtimeReleasePath("/v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish", map[string]string{"endpoint_id": endpointID, "channel": channel})
	headers := http.Header{}
	query := url.Values{}
	if options != nil {
		if options.IdempotencyKey != nil {
			headers.Set("Idempotency-Key", fmt.Sprint(*options.IdempotencyKey))
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if headers.Get("Idempotency-Key") == "" {
		var zero RealtimePublisherPublishResponse
		return zero, fmt.Errorf("Idempotency-Key is required")
	}
	var out RealtimePublisherPublishResponse
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ListRealtimeWorkerCredentials calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials.
func (c *Client) ListRealtimeWorkerCredentials(ctx context.Context, slug string, id string, channel string, group string) ([]RealtimeWorkerCredential, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out []RealtimeWorkerCredential
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// IssueRealtimeWorkerCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials.
func (c *Client) IssueRealtimeWorkerCredential(ctx context.Context, slug string, id string, channel string, group string, request RealtimeWorkerCredentialRequest) (RealtimeWorkerCredentialIssued, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeWorkerCredentialIssued
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// RotateRealtimeWorkerCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/rotate.
func (c *Client) RotateRealtimeWorkerCredential(ctx context.Context, slug string, id string, channel string, group string, credentialID string, request RealtimeWorkerCredentialRotationRequest) (RealtimeWorkerCredentialIssued, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/rotate", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group, "credential_id": credentialID})
	headers := http.Header{}
	var out RealtimeWorkerCredentialIssued
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// RevokeRealtimeWorkerCredential calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke.
func (c *Client) RevokeRealtimeWorkerCredential(ctx context.Context, slug string, id string, channel string, group string, credentialID string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group, "credential_id": credentialID})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, nil, nil, headers)
}

// GetRealtimeWorkerGroup calls GET /v1/realtime/workers/groups/{group_id}.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) GetRealtimeWorkerGroup(ctx context.Context, groupID string) (RealtimeConsumerGroup, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroup
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// DeleteRealtimeWorkerGroup calls DELETE /v1/realtime/workers/groups/{group_id}.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) DeleteRealtimeWorkerGroup(ctx context.Context, groupID string) error {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}", map[string]string{"group_id": groupID})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "DELETE", path, nil, nil, headers)
}

// ClaimRealtimeConsumerGroupBatch calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/batch.
func (c *Client) ClaimRealtimeConsumerGroupBatch(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupBatchRequest) (RealtimeConsumerGroupBatch, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/batch", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupBatch
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ClaimRealtimeWorkerGroupBatch calls POST /v1/realtime/workers/groups/{group_id}/claims/batch.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ClaimRealtimeWorkerGroupBatch(ctx context.Context, groupID string, request RealtimeWorkerBatchClaimRequest) (RealtimeConsumerGroupBatch, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/claims/batch", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupBatch
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// PauseRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/pause.
func (c *Client) PauseRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupPauseState, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/pause", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupPauseState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// ResumeRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resume.
func (c *Client) ResumeRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupPauseState, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resume", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupPauseState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// PauseRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/pause.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) PauseRealtimeWorkerGroup(ctx context.Context, groupID string) (RealtimeConsumerGroupPauseState, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/pause", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupPauseState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// ResumeRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/resume.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ResumeRealtimeWorkerGroup(ctx context.Context, groupID string) (RealtimeConsumerGroupPauseState, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/resume", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupPauseState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// DrainRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/drain.
func (c *Client) DrainRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupDrainState, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/drain", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupDrainState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// DrainRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/drain.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) DrainRealtimeWorkerGroup(ctx context.Context, groupID string) (RealtimeConsumerGroupDrainState, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/drain", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupDrainState
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// SettleRealtimeConsumerGroupBatch calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle/batch.
func (c *Client) SettleRealtimeConsumerGroupBatch(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupBatchSettlementRequest) (RealtimeConsumerGroupBatchSettlementResult, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle/batch", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupBatchSettlementResult
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// SettleRealtimeWorkerGroupBatch calls POST /v1/realtime/workers/groups/{group_id}/claims/settle/batch.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) SettleRealtimeWorkerGroupBatch(ctx context.Context, groupID string, request RealtimeConsumerGroupBatchSettlementRequest) (RealtimeConsumerGroupBatchSettlementResult, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/claims/settle/batch", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupBatchSettlementResult
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupDeliveryHistory calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/messages/{sequence}/history.
func (c *Client) GetRealtimeConsumerGroupDeliveryHistory(ctx context.Context, slug string, id string, channel string, group string, sequence string, options *GetRealtimeConsumerGroupDeliveryHistoryOptions) (RealtimeConsumerGroupDeliveryHistory, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/messages/{sequence}/history", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group, "sequence": sequence})
	headers := http.Header{}
	query := url.Values{}
	if options != nil {
		if options.AfterID != nil {
			query.Set("after_id", fmt.Sprint(*options.AfterID))
		}
		if options.Limit != nil {
			query.Set("limit", fmt.Sprint(*options.Limit))
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out RealtimeConsumerGroupDeliveryHistory
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupCircuit calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit.
func (c *Client) GetRealtimeConsumerGroupCircuit(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupCircuitStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupCircuitStatus
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ConfigureRealtimeConsumerGroupCircuit calls PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit.
func (c *Client) ConfigureRealtimeConsumerGroupCircuit(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupCircuitPolicy) (RealtimeConsumerGroupCircuitStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupCircuitStatus
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupPriority calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/priority.
func (c *Client) GetRealtimeConsumerGroupPriority(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupPriorityStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/priority", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupPriorityStatus
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupMetrics calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/metrics.
func (c *Client) GetRealtimeConsumerGroupMetrics(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupMetrics, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/metrics", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupMetrics
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupWorkerHealth calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats.
func (c *Client) GetRealtimeConsumerGroupWorkerHealth(ctx context.Context, slug string, id string, channel string, group string) (RealtimeWorkerHealth, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeWorkerHealth
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// HeartbeatRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats.
func (c *Client) HeartbeatRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string, request RealtimeWorkerHeartbeatRequest) (RealtimeWorkerHeartbeat, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeWorkerHeartbeat
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// HeartbeatRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/heartbeats.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) HeartbeatRealtimeWorkerGroup(ctx context.Context, groupID string, request RealtimeWorkerHeartbeatRequest) (RealtimeWorkerHeartbeat, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/heartbeats", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeWorkerHeartbeat
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupClaimRate calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate.
func (c *Client) GetRealtimeConsumerGroupClaimRate(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupClaimRateStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupClaimRateStatus
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ConfigureRealtimeConsumerGroupClaimRate calls PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate.
func (c *Client) ConfigureRealtimeConsumerGroupClaimRate(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupClaimRatePolicy) (RealtimeConsumerGroupClaimRateStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupClaimRateStatus
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupRetryPolicy calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy.
func (c *Client) GetRealtimeConsumerGroupRetryPolicy(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupRetryStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupRetryStatus
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ConfigureRealtimeConsumerGroupRetryPolicy calls PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy.
func (c *Client) ConfigureRealtimeConsumerGroupRetryPolicy(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupRetryPolicyRequest) (RealtimeConsumerGroupRetryStatus, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupRetryStatus
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupDeadLetterRetention calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention.
func (c *Client) GetRealtimeConsumerGroupDeadLetterRetention(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupRetentionPolicy, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupRetentionPolicy
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ConfigureRealtimeConsumerGroupDeadLetterRetention calls PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention.
func (c *Client) ConfigureRealtimeConsumerGroupDeadLetterRetention(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupRetentionRequest) (RealtimeConsumerGroupRetentionPolicy, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupRetentionPolicy
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroupLagAlert calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert.
func (c *Client) GetRealtimeConsumerGroupLagAlert(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroupLagAlert, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupLagAlert
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ConfigureRealtimeConsumerGroupLagAlert calls PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert.
func (c *Client) ConfigureRealtimeConsumerGroupLagAlert(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupLagAlertConfig) (RealtimeConsumerGroupLagAlert, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupLagAlert
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// ClaimRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/claims.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ClaimRealtimeWorkerGroup(ctx context.Context, groupID string, request RealtimeWorkerClaimRequest) (RealtimeConsumerGroupClaim, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/claims", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupClaim
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// SettleRealtimeWorkerGroupClaim calls POST /v1/realtime/workers/groups/{group_id}/claims/settle.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) SettleRealtimeWorkerGroupClaim(ctx context.Context, groupID string, request RealtimeConsumerGroupSettlement) (RealtimeConsumerGroupSettlementResult, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/claims/settle", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out RealtimeConsumerGroupSettlementResult
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ListRealtimeWorkerGroupDeadLetters calls GET /v1/realtime/workers/groups/{group_id}/dead-letters.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ListRealtimeWorkerGroupDeadLetters(ctx context.Context, groupID string) ([]RealtimeConsumerGroupDeadLetter, error) {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/dead-letters", map[string]string{"group_id": groupID})
	headers := http.Header{}
	var out []RealtimeConsumerGroupDeadLetter
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ReplayRealtimeWorkerGroupDeadLetter calls POST /v1/realtime/workers/groups/{group_id}/dead-letters/{sequence}/replay.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ReplayRealtimeWorkerGroupDeadLetter(ctx context.Context, groupID string, sequence string) error {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/dead-letters/{sequence}/replay", map[string]string{"group_id": groupID, "sequence": sequence})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, nil, nil, headers)
}

// ResyncRealtimeWorkerGroup calls POST /v1/realtime/workers/groups/{group_id}/resync.
// Configure a separate Client with the endpoint/group scoped credential.
func (c *Client) ResyncRealtimeWorkerGroup(ctx context.Context, groupID string, request RealtimeConsumerGroupResyncRequest) error {
	path := realtimeReleasePath("/v1/realtime/workers/groups/{group_id}/resync", map[string]string{"group_id": groupID})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, request, nil, headers)
}

// ListRealtimeConsumerGroups calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups.
func (c *Client) ListRealtimeConsumerGroups(ctx context.Context, slug string, id string, channel string) ([]RealtimeConsumerGroup, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups", map[string]string{"slug": slug, "id": id, "channel": channel})
	headers := http.Header{}
	var out []RealtimeConsumerGroup
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// CreateRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups.
func (c *Client) CreateRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, request RealtimeConsumerGroupCreateRequest) (RealtimeConsumerGroup, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups", map[string]string{"slug": slug, "id": id, "channel": channel})
	headers := http.Header{}
	var out RealtimeConsumerGroup
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// GetRealtimeConsumerGroup calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}.
func (c *Client) GetRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string) (RealtimeConsumerGroup, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroup
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// DeleteRealtimeConsumerGroup calls DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}.
func (c *Client) DeleteRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "DELETE", path, nil, nil, headers)
}

// ClaimRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims.
func (c *Client) ClaimRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupClaimRequest) (RealtimeConsumerGroupClaim, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupClaim
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// SettleRealtimeConsumerGroupClaim calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle.
func (c *Client) SettleRealtimeConsumerGroupClaim(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupSettlement) (RealtimeConsumerGroupSettlementResult, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeConsumerGroupSettlementResult
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ListRealtimeConsumerGroupDeadLetters calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters.
func (c *Client) ListRealtimeConsumerGroupDeadLetters(ctx context.Context, slug string, id string, channel string, group string) ([]RealtimeConsumerGroupDeadLetter, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out []RealtimeConsumerGroupDeadLetter
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ExportRealtimeConsumerGroupDeadLetters calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export.
func (c *Client) ExportRealtimeConsumerGroupDeadLetters(ctx context.Context, slug string, id string, channel string, group string, options *ExportRealtimeConsumerGroupDeadLettersOptions, dst io.Writer) (int64, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	query := url.Values{}
	if options != nil {
		if options.AfterSequence != nil {
			query.Set("after_sequence", fmt.Sprint(*options.AfterSequence))
		}
		if options.GroupID != nil {
			query.Set("group_id", fmt.Sprint(*options.GroupID))
		}
		if options.ThroughSequence != nil {
			query.Set("through_sequence", fmt.Sprint(*options.ThroughSequence))
		}
		if options.SnapshotAt != nil {
			query.Set("snapshot_at", fmt.Sprint(options.SnapshotAt.Format("2006-01-02T15:04:05.999999999Z07:00")))
		}
		if options.FailureCode != nil {
			for _, value := range *options.FailureCode {
				query.Add("failure_code", fmt.Sprint(value))
			}
		}
		if options.Limit != nil {
			query.Set("limit", fmt.Sprint(*options.Limit))
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.streamRealtimeRelease(ctx, path, dst, headers)
}

// PreviewRealtimeConsumerGroupDeadLetters calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/preview.
func (c *Client) PreviewRealtimeConsumerGroupDeadLetters(ctx context.Context, slug string, id string, channel string, group string, request RealtimeDeadLetterPreviewRequest) (RealtimeDeadLetterPreview, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/preview", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeDeadLetterPreview
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ListRealtimeReplayJobs calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs.
func (c *Client) ListRealtimeReplayJobs(ctx context.Context, slug string, id string, channel string, group string) ([]RealtimeReplayJob, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out []RealtimeReplayJob
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// CreateRealtimeReplayJob calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs.
func (c *Client) CreateRealtimeReplayJob(ctx context.Context, slug string, id string, channel string, group string, request RealtimeReplayJobRequest) (RealtimeReplayJob, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeReplayJob
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// CreateRealtimeReplayDryRun calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/dry-run.
func (c *Client) CreateRealtimeReplayDryRun(ctx context.Context, slug string, id string, channel string, group string, request RealtimeReplayJobRequest) (RealtimeReplayJob, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/dry-run", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeReplayJob
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ControlRealtimeReplayJob calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/{job}/{action}.
func (c *Client) ControlRealtimeReplayJob(ctx context.Context, slug string, id string, channel string, group string, job string, action string) (RealtimeReplayJob, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/{job}/{action}", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group, "job": job, "action": action})
	headers := http.Header{}
	var out RealtimeReplayJob
	err := c.doRealtimeRelease(ctx, "POST", path, nil, &out, headers)
	return out, err
}

// BulkReplayRealtimeConsumerGroupDeadLetters calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/replay.
func (c *Client) BulkReplayRealtimeConsumerGroupDeadLetters(ctx context.Context, slug string, id string, channel string, group string, request RealtimeDeadLetterBulkReplayRequest) (RealtimeDeadLetterBulkReplayResult, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/replay", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	var out RealtimeDeadLetterBulkReplayResult
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// ReplayRealtimeConsumerGroupDeadLetter calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/{sequence}/replay.
func (c *Client) ReplayRealtimeConsumerGroupDeadLetter(ctx context.Context, slug string, id string, channel string, group string, sequence string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/{sequence}/replay", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group, "sequence": sequence})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, nil, nil, headers)
}

// ResyncRealtimeConsumerGroup calls POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resync.
func (c *Client) ResyncRealtimeConsumerGroup(ctx context.Context, slug string, id string, channel string, group string, request RealtimeConsumerGroupResyncRequest) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resync", map[string]string{"slug": slug, "id": id, "channel": channel, "group": group})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "POST", path, request, nil, headers)
}

// GetRealtimeChannelAcknowledgement calls GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement.
func (c *Client) GetRealtimeChannelAcknowledgement(ctx context.Context, slug string, id string, channel string, options *GetRealtimeChannelAcknowledgementOptions) (RealtimeAcknowledgement, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement", map[string]string{"slug": slug, "id": id, "channel": channel})
	headers := http.Header{}
	query := url.Values{}
	if options != nil {
		if options.Principal != nil {
			query.Set("principal", fmt.Sprint(*options.Principal))
		}
		if options.Subscription != nil {
			query.Set("subscription", fmt.Sprint(*options.Subscription))
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if query.Get("principal") == "" {
		var zero RealtimeAcknowledgement
		return zero, fmt.Errorf("principal is required")
	}
	if query.Get("subscription") == "" {
		var zero RealtimeAcknowledgement
		return zero, fmt.Errorf("subscription is required")
	}
	var out RealtimeAcknowledgement
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// GetRealtimeChannelWebhookFilter calls GET /v1/apps/{slug}/webhooks/{id}/realtime-channel.
func (c *Client) GetRealtimeChannelWebhookFilter(ctx context.Context, slug string, id string) (RealtimeChannelWebhookFilter, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/webhooks/{id}/realtime-channel", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	var out RealtimeChannelWebhookFilter
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// SetRealtimeChannelWebhookFilter calls PUT /v1/apps/{slug}/webhooks/{id}/realtime-channel.
func (c *Client) SetRealtimeChannelWebhookFilter(ctx context.Context, slug string, id string, request RealtimeChannelWebhookFilter) (RealtimeChannelWebhookFilter, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/webhooks/{id}/realtime-channel", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	var out RealtimeChannelWebhookFilter
	err := c.doRealtimeRelease(ctx, "PUT", path, request, &out, headers)
	return out, err
}

// DeleteRealtimeChannelWebhookFilter calls DELETE /v1/apps/{slug}/webhooks/{id}/realtime-channel.
func (c *Client) DeleteRealtimeChannelWebhookFilter(ctx context.Context, slug string, id string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/webhooks/{id}/realtime-channel", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "DELETE", path, nil, nil, headers)
}

// IssueRealtimeSubscriptionTicket calls POST /v1/apps/{slug}/realtime/endpoints/{id}/tickets.
func (c *Client) IssueRealtimeSubscriptionTicket(ctx context.Context, slug string, id string, request RealtimeTicketRequest) (RealtimeTicketResponse, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/tickets", map[string]string{"slug": slug, "id": id})
	headers := http.Header{}
	var out RealtimeTicketResponse
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// GetRealtimeUsage calls GET /v1/usage/realtime for the last hours UTC hours
// (zero uses the server default of 24).
func (c *Client) GetRealtimeUsage(ctx context.Context, hours int) (RealtimeUsageResponse, error) {
	path := "/v1/usage/realtime"
	if hours > 0 {
		path += "?hours=" + strconv.Itoa(hours)
	}
	var out RealtimeUsageResponse
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, http.Header{})
	return out, err
}

// GetManagedRealtimeOccupancy calls GET /v1/apps/{slug}/realtime/endpoints/{id}/occupancy
// for up to RealtimeOccupancyChannelsMax channels (ADR-1324).
func (c *Client) GetManagedRealtimeOccupancy(ctx context.Context, slug string, id string, channels []string) (ManagedRealtimeOccupancyResponse, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/occupancy", map[string]string{"slug": slug, "id": id}) +
		"?" + url.Values{"channel": channels}.Encode()
	var out ManagedRealtimeOccupancyResponse
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, http.Header{})
	return out, err
}

// RevokeRealtimeSubscriptionTickets calls DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets?principal=.
func (c *Client) RevokeRealtimeSubscriptionTickets(ctx context.Context, slug string, id string, principal string) (RealtimeTicketsRevokeResponse, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/tickets", map[string]string{"slug": slug, "id": id}) +
		"?principal=" + url.QueryEscape(principal)
	headers := http.Header{}
	var out RealtimeTicketsRevokeResponse
	err := c.doRealtimeRelease(ctx, "DELETE", path, nil, &out, headers)
	return out, err
}

// RevokeRealtimeSubscriptionTicket calls DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}.
func (c *Client) RevokeRealtimeSubscriptionTicket(ctx context.Context, slug string, id string, ticket string) error {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}", map[string]string{"slug": slug, "id": id, "ticket": ticket})
	headers := http.Header{}
	return c.doRealtimeRelease(ctx, "DELETE", path, nil, nil, headers)
}

// GetManagedRealtimeAccess calls GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access.
func (c *Client) GetManagedRealtimeAccess(ctx context.Context, slug string, id string, principal string) (ManagedRealtimeAccessResponse, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access", map[string]string{"slug": slug, "id": id, "principal": principal})
	headers := http.Header{}
	var out ManagedRealtimeAccessResponse
	err := c.doRealtimeRelease(ctx, "GET", path, nil, &out, headers)
	return out, err
}

// ChangeManagedRealtimeAccess calls POST /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access.
func (c *Client) ChangeManagedRealtimeAccess(ctx context.Context, slug string, id string, principal string, request ManagedRealtimeAccessRequest) (ManagedRealtimeAccessResponse, error) {
	path := realtimeReleasePath("/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access", map[string]string{"slug": slug, "id": id, "principal": principal})
	headers := http.Header{}
	var out ManagedRealtimeAccessResponse
	err := c.doRealtimeRelease(ctx, "POST", path, request, &out, headers)
	return out, err
}

// PublishWithRealtimePublisherCredentialOptions carries optional query and header parameters.
type PublishWithRealtimePublisherCredentialOptions struct {
	IdempotencyKey *string
}

// GetRealtimeConsumerGroupDeliveryHistoryOptions carries optional query and header parameters.
type GetRealtimeConsumerGroupDeliveryHistoryOptions struct {
	AfterID *string
	Limit   *int
}

// ExportRealtimeConsumerGroupDeadLettersOptions carries optional query and header parameters.
type ExportRealtimeConsumerGroupDeadLettersOptions struct {
	AfterSequence   *string
	GroupID         *string
	ThroughSequence *string
	SnapshotAt      *time.Time
	FailureCode     *[]string
	Limit           *int
}

// GetRealtimeChannelAcknowledgementOptions carries optional query and header parameters.
type GetRealtimeChannelAcknowledgementOptions struct {
	Principal    *string
	Subscription *string
}
