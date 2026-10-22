package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

// durableScopePath preserves selector escaping and exact scope identity.
func durableScopePath(slug, suffix string, scope DurableEntityInspectRequest, cursor string) (string, error) {
	if scope.Namespace == "" || scope.Key == "" {
		return "", errors.New("durable scope requires namespace and key")
	}
	q := url.Values{"namespace": {scope.Namespace}, "key": {scope.Key}}
	if scope.Environment != "" {
		q.Set("environment", scope.Environment)
	}
	if scope.PlatformTenantID != "" {
		q.Set("platform_tenant_id", scope.PlatformTenantID)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "/v1/apps/" + url.PathEscape(slug) + "/entities/" + suffix + "?" + q.Encode(), nil
}

// InspectDurableEntityTenantQuotas returns preview metadata without rounding numeric revisions.
func (c *Client) InspectDurableEntityTenantQuotas(ctx context.Context, slug string, scope DurableEntityInspectRequest) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quotas", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// GetDurableEntityReadiness returns preview metadata without rounding numeric revisions.
func (c *Client) GetDurableEntityReadiness(ctx context.Context, slug string) (json.RawMessage, error) {
	var out json.RawMessage
	callErr := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/entities/readiness", nil, &out)
	return out, callErr
}

// PlanDurableEntityQuotaMigration returns preview metadata without rounding numeric revisions.
func (c *Client) PlanDurableEntityQuotaMigration(ctx context.Context, slug string, scope DurableEntityInspectRequest, cursor string) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-migration/plan", scope, cursor)
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// InspectDurableEntityQuotaMigration returns preview metadata without rounding numeric revisions.
func (c *Client) InspectDurableEntityQuotaMigration(ctx context.Context, slug string, scope DurableEntityInspectRequest) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-migration", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// StartDurableEntityQuotaMigration returns preview metadata without rounding numeric revisions.
func (c *Client) StartDurableEntityQuotaMigration(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-migration/start", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// StepDurableEntityQuotaMigration returns preview metadata without rounding numeric revisions.
func (c *Client) StepDurableEntityQuotaMigration(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-migration/step", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// InspectDurableEntityQuotaPolicy returns preview metadata without rounding numeric revisions.
func (c *Client) InspectDurableEntityQuotaPolicy(ctx context.Context, slug string, scope DurableEntityInspectRequest) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-policy", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// PreviewDurableEntityQuotaPolicy returns preview metadata without rounding numeric revisions.
func (c *Client) PreviewDurableEntityQuotaPolicy(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-policy/preview", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// ApplyDurableEntityQuotaPolicy returns preview metadata without rounding numeric revisions.
func (c *Client) ApplyDurableEntityQuotaPolicy(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "quota-policy/apply", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// ListDurableEntityRegistry returns preview metadata without rounding numeric revisions.
func (c *Client) ListDurableEntityRegistry(ctx context.Context, slug string, scope DurableEntityInspectRequest, cursor string) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "registry", scope, cursor)
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// EnableDurableEntityRegistry returns preview metadata without rounding numeric revisions.
func (c *Client) EnableDurableEntityRegistry(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "registry/enable", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// InspectDurableEntityRegistryCleanup returns preview metadata without rounding numeric revisions.
func (c *Client) InspectDurableEntityRegistryCleanup(ctx context.Context, slug string, scope DurableEntityInspectRequest) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "registry/cleanup", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// StartDurableEntityRegistryCleanup returns preview metadata without rounding numeric revisions.
func (c *Client) StartDurableEntityRegistryCleanup(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "registry/cleanup/start", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// StepDurableEntityRegistryCleanup returns preview metadata without rounding numeric revisions.
func (c *Client) StepDurableEntityRegistryCleanup(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "registry/cleanup/step", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// PreviewDurableEntityRetirement returns preview metadata without rounding numeric revisions.
func (c *Client) PreviewDurableEntityRetirement(ctx context.Context, slug string, scope DurableEntityInspectRequest) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "retirement", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "GET", path, nil, &out)
	return out, callErr
}

// StartDurableEntityRetirement returns preview metadata without rounding numeric revisions.
func (c *Client) StartDurableEntityRetirement(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "retirement/start", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// StepDurableEntityRetirement returns preview metadata without rounding numeric revisions.
func (c *Client) StepDurableEntityRetirement(ctx context.Context, slug string, scope DurableEntityInspectRequest, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path, err := durableScopePath(slug, "retirement/step", scope, "")
	if err != nil {
		return nil, err
	}
	callErr := c.do(ctx, "POST", path, request, &out)
	return out, callErr
}

// MintDurableEntitySessionGrant returns preview metadata without rounding numeric revisions.
func (c *Client) MintDurableEntitySessionGrant(ctx context.Context, slug string, request json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	callErr := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/session-grants", request, &out)
	return out, callErr
}
func (c *Client) RevokeDurableEntitySessionGrant(ctx context.Context, slug, grant string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+url.PathEscape(slug)+"/entities/session-grants/"+url.PathEscape(grant), nil, nil)
}
