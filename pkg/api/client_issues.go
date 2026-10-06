package api

import (
	"context"
	"net/url"
	"strconv"
)

func issueAppPath(slug string) string { return "/v1/apps/" + url.PathEscape(slug) }
func (c *Client) ListIssues(ctx context.Context, slug, state, environment, cursor string) (ListIssuesResponse, error) {
	return c.ListIssuesFiltered(ctx, slug, state, environment, "", cursor)
}

type IssueListOptions struct {
	State, Environment, Assignee, Sort, Cursor string
	MinCustomers                               int64
}

// ListIssuesFiltered lists issues with optional state, environment, and owner filters.
// Assignee accepts "me", "unassigned", or an account UUID.
func (c *Client) ListIssuesFiltered(ctx context.Context, slug, state, environment, assignee, cursor string) (ListIssuesResponse, error) {
	return c.ListIssuesWithOptions(ctx, slug, IssueListOptions{State: state, Environment: environment, Assignee: assignee, Cursor: cursor})
}

// ListIssuesWithOptions lists grouped issues with ownership and impact ordering filters.
func (c *Client) ListIssuesWithOptions(ctx context.Context, slug string, options IssueListOptions) (ListIssuesResponse, error) {
	q := url.Values{}
	q.Set("state", options.State)
	q.Set("environment", options.Environment)
	if options.Assignee != "" {
		q.Set("assignee", options.Assignee)
	}
	if options.Sort != "" {
		q.Set("sort", options.Sort)
	}
	if options.MinCustomers > 0 {
		q.Set("min_customers", strconv.FormatInt(options.MinCustomers, 10))
	}
	q.Set("cursor", options.Cursor)
	var out ListIssuesResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetIssueImpactAlertPolicy(ctx context.Context, slug string) (IssueImpactAlertPolicy, error) {
	var out IssueImpactAlertPolicy
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-impact-alert-policy", nil, &out)
	return out, err
}

func (c *Client) SetIssueImpactAlertPolicy(ctx context.Context, slug string, in UpdateIssueImpactAlertPolicyRequest) (IssueImpactAlertPolicy, error) {
	var out IssueImpactAlertPolicy
	err := c.do(ctx, "PUT", issueAppPath(slug)+"/issue-impact-alert-policy", in, &out)
	return out, err
}
func (c *Client) GetIssueOwnershipRules(ctx context.Context, slug string) (IssueOwnershipRules, error) {
	var out IssueOwnershipRules
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-ownership-rules", nil, &out)
	return out, err
}
func (c *Client) SetIssueOwnershipRules(ctx context.Context, slug string, in IssueOwnershipRules) (IssueOwnershipRules, error) {
	var out IssueOwnershipRules
	err := c.do(ctx, "PUT", issueAppPath(slug)+"/issue-ownership-rules", in, &out)
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
