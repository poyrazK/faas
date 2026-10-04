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

func (c *Client) GetApplicationStandardEnrollment(ctx context.Context, org, appID string) (ApplicationStandardEnrollment, error) {
	var result ApplicationStandardEnrollment
	path := standardResourcePath(org, "enrollments") + "/" + url.PathEscape(appID)
	err := c.do(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func standardResourcePath(org, kind string) string {
	return "/v1/orgs/" + url.PathEscape(org) + "/application-standard-" + kind
}

func standardResourceQuery(after string, limit int) string {
	query := url.Values{}
	if after != "" {
		query.Set("after", after)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return "?" + query.Encode()
}

func (c *Client) CreateApplicationStandardLogDestination(ctx context.Context, org string, request CreateApplicationStandardLogDestinationRequest) (ApplicationStandardLogDestination, error) {
	var result ApplicationStandardLogDestination
	err := c.do(ctx, http.MethodPost, standardResourcePath(org, "log-destinations"), request, &result)
	return result, err
}

func (c *Client) GetApplicationStandardLogDestination(ctx context.Context, org, id string) (ApplicationStandardLogDestination, error) {
	var result ApplicationStandardLogDestination
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "log-destinations")+"/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) ListApplicationStandardLogDestinations(ctx context.Context, org, after string, limit int) (ApplicationStandardLogDestinationList, error) {
	var result ApplicationStandardLogDestinationList
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "log-destinations")+standardResourceQuery(after, limit), nil, &result)
	return result, err
}

func (c *Client) CreateApplicationStandardPublisher(ctx context.Context, org string, request CreateApplicationStandardPublisherRequest) (ApplicationStandardPublisher, error) {
	var result ApplicationStandardPublisher
	err := c.do(ctx, http.MethodPost, standardResourcePath(org, "publishers"), request, &result)
	return result, err
}

func (c *Client) GetApplicationStandardPublisher(ctx context.Context, org, id string) (ApplicationStandardPublisher, error) {
	var result ApplicationStandardPublisher
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "publishers")+"/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) ListApplicationStandardPublishers(ctx context.Context, org, after string, limit int) (ApplicationStandardPublisherList, error) {
	var result ApplicationStandardPublisherList
	err := c.do(ctx, http.MethodGet, standardResourcePath(org, "publishers")+standardResourceQuery(after, limit), nil, &result)
	return result, err
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
