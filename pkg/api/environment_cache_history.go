package api

import (
	"context"
	"net/url"
	"strconv"
)

type EnvironmentCachePolicyHistory struct {
	AppID              string                   `json:"app_id"`
	ProjectID          string                   `json:"project_id"`
	EnvironmentID      string                   `json:"environment_id"`
	Scope              string                   `json:"scope"`
	CurrentRevision    int64                    `json:"current_revision"`
	Entries            []EnvironmentCachePolicy `json:"entries"`
	NextBeforeRevision *int64                   `json:"next_before_revision,omitempty"`
}

func (c *Client) ListEnvironmentCachePolicyHistory(ctx context.Context, slug, environment string, before int64, limit int) (EnvironmentCachePolicyHistory, error) {
	var out EnvironmentCachePolicyHistory
	path := "/v1/apps/" + url.PathEscape(slug) + "/cache-policy/history?environment=" + url.QueryEscape(environment) + "&before_revision=" + strconv.FormatInt(before, 10) + "&limit=" + strconv.Itoa(limit)
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
func (c *Client) GetEnvironmentCachePolicyRevision(ctx context.Context, slug, environment string, revision int64) (EnvironmentCachePolicy, error) {
	var out EnvironmentCachePolicy
	path := "/v1/apps/" + url.PathEscape(slug) + "/cache-policy/history/" + strconv.FormatInt(revision, 10) + "?environment=" + url.QueryEscape(environment)
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
