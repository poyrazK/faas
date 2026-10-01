package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

func flagsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/flags"
}

// ProjectFlags returns the immutable bundle and its current version.
func (c *Client) ProjectFlags(ctx context.Context, project, environment string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment), nil, &out)
	return out, err
}

// PublishProjectFlags requires an expected_version and full config JSON body.
func (c *Client) PublishProjectFlags(ctx context.Context, project, environment string, body json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPut, flagsPath(project, environment), body, &out)
	return out, err
}
func (c *Client) ProjectFlagVersions(ctx context.Context, project, environment string, before int64) (json.RawMessage, error) {
	var out json.RawMessage
	p := flagsPath(project, environment) + "/versions"
	if before > 0 {
		p += "?before_version=" + strconv.FormatInt(before, 10)
	}
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	return out, err
}
func (c *Client) InspectProjectFlag(ctx context.Context, project, environment, key, customer string, version int64, fallbackVariants ...string) (json.RawMessage, error) {
	var out json.RawMessage
	var fallbackVariant string
	if len(fallbackVariants) > 0 {
		fallbackVariant = fallbackVariants[0]
	}
	err := c.do(ctx, http.MethodPost, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/inspect", map[string]any{"customer_id": customer, "version": version, "fallback": false, "fallback_variant": fallbackVariant}, &out)
	return out, err
}
func (c *Client) RollbackProjectFlags(ctx context.Context, project, environment string, expected, version int64) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, flagsPath(project, environment)+"/rollback", map[string]int64{"expected_version": expected, "version": version}, &out)
	return out, err
}
func (c *Client) ProjectFlagRequests(ctx context.Context, project, environment, key string, query url.Values) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/requests?"+query.Encode(), nil, &out)
	return out, err
}

// RuntimeFlags requires a Client constructed with a workload JWT for gregale:flags.
// Account API keys cannot read this endpoint.
func (c *Client) RuntimeFlags(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, "/v1/runtime/flags", nil, &out)
	return out, err
}
