package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func orgQueueAppPath(organization, slug string) string {
	return "/v1/orgs/" + url.PathEscape(organization) + "/apps/" + url.PathEscape(slug)
}
func orgQueuePage(path string, limit int, before string) string {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	if len(q) > 0 {
		return path + "?" + q.Encode()
	}
	return path
}
func (c *Client) ListOrgQueueBindings(ctx context.Context, organization, slug string) ([]QueueBindingResponse, error) {
	var out []QueueBindingResponse
	err := c.do(ctx, http.MethodGet, orgQueueAppPath(organization, slug)+"/queue-bindings", nil, &out)
	return out, err
}
func (c *Client) CreateOrgQueueBinding(ctx context.Context, organization, slug string, req CreateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queue-bindings", req, &out)
	return out, err
}
func (c *Client) GetOrgQueueBinding(ctx context.Context, organization, slug, id string) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	err := c.do(ctx, http.MethodGet, orgQueueAppPath(organization, slug)+"/queue-bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) GetOrgQueueBindingStatus(ctx context.Context, organization, slug, id string) (QueueBindingStatusResponse, error) {
	var out QueueBindingStatusResponse
	err := c.do(ctx, http.MethodGet, orgQueueAppPath(organization, slug)+"/queue-bindings/"+url.PathEscape(id)+"/status", nil, &out)
	return out, err
}
func (c *Client) UpdateOrgQueueBinding(ctx context.Context, organization, slug, id string, req UpdateQueueBindingRequest) (QueueBindingResponse, error) {
	var out QueueBindingResponse
	err := c.do(ctx, http.MethodPatch, orgQueueAppPath(organization, slug)+"/queue-bindings/"+url.PathEscape(id), req, &out)
	return out, err
}
func (c *Client) DeleteOrgQueueBinding(ctx context.Context, organization, slug, id string) error {
	return c.do(ctx, http.MethodDelete, orgQueueAppPath(organization, slug)+"/queue-bindings/"+url.PathEscape(id), nil, nil)
}
func (c *Client) SendOrgQueueMessage(ctx context.Context, organization, slug string, req QueueSendRequest) (QueueSendResponse, error) {
	var out QueueSendResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/send", req, &out)
	return out, err
}
func (c *Client) ReceiveOrgQueueResult(ctx context.Context, organization, slug string) (QueueReceiveResponse, error) {
	var out QueueReceiveResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/receive", nil, &out)
	return out, err
}
func (c *Client) AckOrgQueueRow(ctx context.Context, organization, slug, id string) error {
	return c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/"+url.PathEscape(id)+"/ack", nil, nil)
}
func (c *Client) GetOrgQueueState(ctx context.Context, organization, slug string) (QueueStateResponse, error) {
	var out QueueStateResponse
	err := c.do(ctx, http.MethodGet, orgQueueAppPath(organization, slug)+"/queues/state", nil, &out)
	return out, err
}
func (c *Client) PeekOrgQueue(ctx context.Context, organization, slug string, limit int, before string) (QueuePeekResponse, error) {
	var out QueuePeekResponse
	err := c.do(ctx, http.MethodGet, orgQueuePage(orgQueueAppPath(organization, slug)+"/queues/peek", limit, before), nil, &out)
	return out, err
}
func (c *Client) ListOrgQueueDeadLetters(ctx context.Context, organization, slug string, limit int, before string) (QueueDeadLetterResponse, error) {
	var out QueueDeadLetterResponse
	err := c.do(ctx, http.MethodGet, orgQueuePage(orgQueueAppPath(organization, slug)+"/queues/dead_letter", limit, before), nil, &out)
	return out, err
}
func (c *Client) ReplayOrgQueueDeadLetter(ctx context.Context, organization, slug, id string) (AsyncInvokeResponse, error) {
	var out AsyncInvokeResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/dead_letter/"+url.PathEscape(id)+"/replay", nil, &out)
	return out, err
}
func (c *Client) GetOrgQueueUsage(ctx context.Context, organization string) (OrgQueueUsageResponse, error) {
	var out OrgQueueUsageResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(organization)+"/queue-usage", nil, &out)
	return out, err
}
func (c *Client) LeaseOrgQueueMessages(ctx context.Context, organization, slug, queue string, req QueueLeaseRequest) (QueueLeaseResponse, error) {
	var out QueueLeaseResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/"+url.PathEscape(queue)+"/leases", req, &out)
	return out, err
}
func (c *Client) AckOrgQueueLeases(ctx context.Context, organization, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/"+url.PathEscape(queue)+"/leases:ack", req, &out)
	return out, err
}
func (c *Client) NackOrgQueueLeases(ctx context.Context, organization, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/"+url.PathEscape(queue)+"/leases:nack", req, &out)
	return out, err
}
func (c *Client) ExtendOrgQueueLeases(ctx context.Context, organization, slug, queue string, req QueueLeaseReceiptsRequest) (QueueLeaseReceiptsResponse, error) {
	var out QueueLeaseReceiptsResponse
	err := c.do(ctx, http.MethodPost, orgQueueAppPath(organization, slug)+"/queues/"+url.PathEscape(queue)+"/leases:extend", req, &out)
	return out, err
}
func (c *Client) GetOrgProjectEnvironmentQueueBindings(ctx context.Context, organization, project, environment, workload string) (ProjectEnvironmentQueueBindingsResponse, error) {
	var out ProjectEnvironmentQueueBindingsResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(organization)+"/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/workloads/"+url.PathEscape(workload)+"/queue-bindings", nil, &out)
	return out, err
}
func (c *Client) ReplaceOrgProjectEnvironmentQueueBindings(ctx context.Context, organization, project, environment, workload string, req ReplaceProjectEnvironmentQueueBindingsRequest) (ProjectEnvironmentQueueBindingsResponse, error) {
	var out ProjectEnvironmentQueueBindingsResponse
	err := c.do(ctx, http.MethodPut, "/v1/orgs/"+url.PathEscape(organization)+"/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/workloads/"+url.PathEscape(workload)+"/queue-bindings", req, &out)
	return out, err
}
