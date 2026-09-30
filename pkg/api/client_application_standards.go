package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func applicationStandardsPath(org string) string {
	return "/v1/orgs/" + url.PathEscape(org) + "/application-standards"
}

func (c *Client) PublishApplicationStandardVersion(ctx context.Context, org, slug string, request CreateApplicationStandardVersionRequest) (ApplicationStandardVersion, error) {
	var version ApplicationStandardVersion
	err := c.do(ctx, http.MethodPost, applicationStandardsPath(org)+"/"+url.PathEscape(slug)+"/versions", request, &version)
	return version, err
}

func (c *Client) GetApplicationStandardVersion(ctx context.Context, org, slug string, version int64) (ApplicationStandardVersion, error) {
	var result ApplicationStandardVersion
	path := applicationStandardsPath(org) + "/" + url.PathEscape(slug)
	if version > 0 {
		path += "?version=" + strconv.FormatInt(version, 10)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *Client) ListApplicationStandards(ctx context.Context, org, after string, limit int) (ApplicationStandardList, error) {
	var result ApplicationStandardList
	query := url.Values{}
	if after != "" {
		query.Set("after", after)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, http.MethodGet, applicationStandardsPath(org)+"?"+query.Encode(), nil, &result)
	return result, err
}
