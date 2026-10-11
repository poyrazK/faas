package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func readinessSubscriptionQuery(app, scope, tenant string) url.Values {
	q := url.Values{"scope": {scope}}
	if app != "" {
		q.Set("app_id", app)
	}
	if tenant != "" {
		q.Set("tenant_id", tenant)
	}
	return q
}
func (c *Client) CreateAccountWorkflowReadinessSubscription(ctx context.Context, slug string, req OperationWorkflowReadinessSubscriptionRequest) (OperationWorkflowReadinessSubscription, error) {
	var out OperationWorkflowReadinessSubscription
	err := c.doOperation(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions", req, &out)
	return out, err
}
func (c *Client) CreatePlatformTenantSelfWorkflowReadinessSubscription(ctx context.Context, req OperationWorkflowReadinessSubscriptionRequest) (OperationWorkflowReadinessSubscription, error) {
	var out OperationWorkflowReadinessSubscription
	err := c.doOperation(ctx, http.MethodPost, "/v1/platform-tenant-self/workflow-readiness/subscriptions", req, &out)
	return out, err
}
func (c *Client) ListAccountWorkflowReadinessSubscriptions(ctx context.Context, slug, scope, tenant string) (OperationWorkflowReadinessSubscriptionsResponse, error) {
	var out OperationWorkflowReadinessSubscriptionsResponse
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions?"+readinessSubscriptionQuery("", scope, tenant).Encode(), nil, &out)
	return out, err
}
func (c *Client) ListPlatformTenantSelfWorkflowReadinessSubscriptions(ctx context.Context, app, scope string) (OperationWorkflowReadinessSubscriptionsResponse, error) {
	var out OperationWorkflowReadinessSubscriptionsResponse
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-readiness/subscriptions?"+readinessSubscriptionQuery(app, scope, "").Encode(), nil, &out)
	return out, err
}
func (c *Client) DeleteAccountWorkflowReadinessSubscription(ctx context.Context, slug, scope, tenant, id string) error {
	return c.doOperation(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions/"+url.PathEscape(id)+"?"+readinessSubscriptionQuery("", scope, tenant).Encode(), nil, nil)
}
func (c *Client) DeletePlatformTenantSelfWorkflowReadinessSubscription(ctx context.Context, app, scope, id string) error {
	return c.doOperation(ctx, http.MethodDelete, "/v1/platform-tenant-self/workflow-readiness/subscriptions/"+url.PathEscape(id)+"?"+readinessSubscriptionQuery(app, scope, "").Encode(), nil, nil)
}
func (c *Client) GetAccountWorkflowReadinessInbox(ctx context.Context, slug, scope, tenant, id string, after int64, limit int) (OperationWorkflowReadinessInboxResponse, error) {
	var out OperationWorkflowReadinessInboxResponse
	q := readinessSubscriptionQuery("", scope, tenant)
	q.Set("after_sequence", strconv.FormatInt(after, 10))
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) GetPlatformTenantSelfWorkflowReadinessInbox(ctx context.Context, app, scope, id string, after int64, limit int) (OperationWorkflowReadinessInboxResponse, error) {
	var out OperationWorkflowReadinessInboxResponse
	q := readinessSubscriptionQuery(app, scope, "")
	q.Set("after_sequence", strconv.FormatInt(after, 10))
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) UpdateAccountWorkflowReadinessNotification(ctx context.Context, slug, scope, tenant, id string, sequence int64, req OperationWorkflowReadinessCoordinationRequest) (OperationWorkflowReadinessNotification, error) {
	var out OperationWorkflowReadinessNotification
	err := c.doOperation(ctx, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications/"+strconv.FormatInt(sequence, 10)+"?"+readinessSubscriptionQuery("", scope, tenant).Encode(), req, &out)
	return out, err
}

func (c *Client) CheckAccountWorkflowReadinessNotification(ctx context.Context, slug, scope, tenant, id string, sequence int64) (OperationWorkflowReadinessNotificationCheck, error) {
	var out OperationWorkflowReadinessNotificationCheck
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications/"+strconv.FormatInt(sequence, 10)+"?"+readinessSubscriptionQuery("", scope, tenant).Encode(), nil, &out)
	return out, err
}

func (c *Client) UpdatePlatformTenantSelfWorkflowReadinessNotification(ctx context.Context, app, scope, id string, sequence int64, req OperationWorkflowReadinessCoordinationRequest) (OperationWorkflowReadinessNotification, error) {
	var out OperationWorkflowReadinessNotification
	err := c.doOperation(ctx, http.MethodPatch, "/v1/platform-tenant-self/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications/"+strconv.FormatInt(sequence, 10)+"?"+readinessSubscriptionQuery(app, scope, "").Encode(), req, &out)
	return out, err
}

func (c *Client) CheckPlatformTenantSelfWorkflowReadinessNotification(ctx context.Context, app, scope, id string, sequence int64) (OperationWorkflowReadinessNotificationCheck, error) {
	var out OperationWorkflowReadinessNotificationCheck
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-readiness/subscriptions/"+url.PathEscape(id)+"/notifications/"+strconv.FormatInt(sequence, 10)+"?"+readinessSubscriptionQuery(app, scope, "").Encode(), nil, &out)
	return out, err
}

func readinessFollowUpQuery(opts OperationWorkflowReadinessFollowUpOptions) url.Values {
	q := readinessSubscriptionQuery(opts.AppID, opts.Scope, opts.TenantID)
	if opts.Owner != "" {
		q.Set("owner", opts.Owner)
	}
	if opts.Workflow != "" {
		q.Set("workflow", opts.Workflow)
	}
	if opts.Due != "" {
		q.Set("due", opts.Due)
	}
	if opts.Offset != 0 {
		q.Set("offset", strconv.Itoa(opts.Offset))
	}
	if opts.Limit != 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	return q
}
func (c *Client) ListAccountWorkflowReadinessFollowUps(ctx context.Context, slug string, opts OperationWorkflowReadinessFollowUpOptions) (OperationWorkflowReadinessFollowUpsResponse, error) {
	var out OperationWorkflowReadinessFollowUpsResponse
	opts.AppID = ""
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/follow-ups?"+readinessFollowUpQuery(opts).Encode(), nil, &out)
	return out, err
}
func (c *Client) ListPlatformTenantSelfWorkflowReadinessFollowUps(ctx context.Context, opts OperationWorkflowReadinessFollowUpOptions) (OperationWorkflowReadinessFollowUpsResponse, error) {
	var out OperationWorkflowReadinessFollowUpsResponse
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-readiness/follow-ups?"+readinessFollowUpQuery(opts).Encode(), nil, &out)
	return out, err
}
