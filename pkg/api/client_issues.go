package api

import (
	"context"
	"net/url"
)

func issueAppPath(slug string) string { return "/v1/apps/" + url.PathEscape(slug) }
func (c *Client) ListIssues(ctx context.Context, slug, state, environment, cursor string) (ListIssuesResponse, error) {
	return c.ListIssuesFiltered(ctx, slug, state, environment, "", cursor)
}

// ListIssuesFiltered lists issues with optional state, environment, and owner filters.
// Assignee accepts "me", "unassigned", or an account UUID.
func (c *Client) ListIssuesFiltered(ctx context.Context, slug, state, environment, assignee, cursor string) (ListIssuesResponse, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("environment", environment)
	if assignee != "" {
		q.Set("assignee", assignee)
	}
	q.Set("cursor", cursor)
	var out ListIssuesResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) GetIssue(ctx context.Context, slug, id, since, cursor string) (IssueDetail, error) {
	return c.GetIssuePage(ctx, slug, id, since, cursor, "", "")
}
func (c *Client) GetIssuePage(ctx context.Context, slug, id, since, eventCursor, releaseCursor, activityCursor string) (IssueDetail, error) {
	q := url.Values{}
	q.Set("release_cursor", releaseCursor)
	q.Set("activity_cursor", activityCursor)
	q.Set("since", since)
	q.Set("event_cursor", eventCursor)
	var out IssueDetail
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issues/"+url.PathEscape(id)+"?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) ActOnIssue(ctx context.Context, slug, id string, in IssueActionRequest) (Issue, error) {
	var out Issue
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issues/"+url.PathEscape(id)+"/actions", in, &out)
	return out, err
}
func (c *Client) CreateIssueIngestToken(ctx context.Context, slug string, in CreateIssueIngestTokenRequest) (IssueIngestToken, error) {
	var out IssueIngestToken
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issue-ingest-tokens", in, &out)
	return out, err
}
func (c *Client) ListIssueIngestTokens(ctx context.Context, slug string) ([]IssueIngestToken, error) {
	var out ListIssueIngestTokensResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-ingest-tokens", nil, &out)
	return out.Items, err
}
func (c *Client) RevokeIssueIngestToken(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", issueAppPath(slug)+"/issue-ingest-tokens/"+url.PathEscape(id), nil, nil)
}
func (c *Client) IngestIssueEvent(ctx context.Context, slug string, in IssueEvent) (IssueEventResponse, error) {
	var out IssueEventResponse
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issue-events", in, &out)
	return out, err
}

// IngestIssueOTLP submits a JSON export using a deployment-bound reporting token.
func (c *Client) IngestIssueOTLP(ctx context.Context, slug, signal string, in any) error {
	return c.do(ctx, "POST", issueAppPath(slug)+"/issue-events/otlp/"+url.PathEscape(signal), in, nil)
}
