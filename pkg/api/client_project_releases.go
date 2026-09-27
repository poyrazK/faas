package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func projectReleaseSetsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/release-sets"
}

func (c *Client) GetActiveProjectReleaseSet(ctx context.Context, project, environment string) (ProjectReleaseSetResponse, error) {
	var out ProjectReleaseSetResponse
	return out, c.do(ctx, http.MethodGet, projectReleaseSetsPath(project, environment)+"/active", nil, &out)
}

func (c *Client) GetProjectReleaseSet(ctx context.Context, project, environment, id string) (ProjectReleaseSetResponse, error) {
	var out ProjectReleaseSetResponse
	return out, c.do(ctx, http.MethodGet, projectReleaseSetsPath(project, environment)+"/"+url.PathEscape(id), nil, &out)
}

func (c *Client) ListProjectReleaseSets(ctx context.Context, project, environment, before string, limit int) (ProjectReleaseSetListResponse, error) {
	var out ProjectReleaseSetListResponse
	path := projectReleaseSetsPath(project, environment)
	query := url.Values{}
	if before != "" {
		query.Set("before", before)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
