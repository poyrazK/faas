package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func issueAppPath(slug string) string { return "/v1/apps/" + url.PathEscape(slug) }
func issueSourceMapsPath(slug, deployment string) string {
	return issueAppPath(slug) + "/deployments/" + url.PathEscape(deployment) + "/issue-source-maps"
}
func (c *Client) PublishIssueSourceMaps(ctx context.Context, slug, deployment string, in PublishIssueSourceMapsRequest) (IssueSourceMapsMetadata, error) {
	var out IssueSourceMapsMetadata
	err := c.do(ctx, "PUT", issueSourceMapsPath(slug, deployment), in, &out)
	return out, err
}
func (c *Client) GetIssueSourceMaps(ctx context.Context, slug, deployment string) (IssueSourceMapsMetadata, error) {
	var out IssueSourceMapsMetadata
	err := c.do(ctx, "GET", issueSourceMapsPath(slug, deployment), nil, &out)
	return out, err
}
func (c *Client) RetireIssueSourceMaps(ctx context.Context, slug, deployment string) (IssueSourceMapsMetadata, error) {
	var out IssueSourceMapsMetadata
	err := c.do(ctx, "DELETE", issueSourceMapsPath(slug, deployment), nil, &out)
	return out, err
}
func (c *Client) ListIssues(ctx context.Context, slug, state, environment, cursor string) (ListIssuesResponse, error) {
	return c.ListIssuesFiltered(ctx, slug, state, environment, "", cursor)
}

func (c *Client) ListIssuesFiltered(ctx context.Context, slug, state, environment, assignee, cursor string) (ListIssuesResponse, error) {
	return c.ListIssuesWithOptions(ctx, slug, state, environment, assignee, "", 0, cursor)
}

func (c *Client) ListIssuesWithOptions(ctx context.Context, slug, state, environment, assignee, sortBy string, minCustomers int64, cursor string) (ListIssuesResponse, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("environment", environment)
	if assignee != "" {
		q.Set("assignee", assignee)
	}
	if sortBy != "" {
		q.Set("sort", sortBy)
	}
	if minCustomers > 0 {
		q.Set("min_customers", strconv.FormatInt(minCustomers, 10))
	}
	q.Set("cursor", cursor)
	var out ListIssuesResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetIssueImpactAlertPolicy(ctx context.Context, slug string) (IssueImpactAlertPolicy, error) {
	var out IssueImpactAlertPolicy
	err := c.do(ctx, http.MethodGet, issueAppPath(slug)+"/issue-impact-alert-policy", nil, &out)
	return out, err
}

func (c *Client) SetIssueImpactAlertPolicy(ctx context.Context, slug string, in UpdateIssueImpactAlertPolicyRequest) (IssueImpactAlertPolicy, error) {
	var out IssueImpactAlertPolicy
	err := c.do(ctx, http.MethodPut, issueAppPath(slug)+"/issue-impact-alert-policy", in, &out)
	return out, err
}

func (c *Client) GetIssueOwnershipRules(ctx context.Context, slug string) (IssueOwnershipRules, error) {
	var out IssueOwnershipRules
	err := c.do(ctx, http.MethodGet, issueAppPath(slug)+"/issue-ownership-rules", nil, &out)
	return out, err
}

func (c *Client) SetIssueOwnershipRules(ctx context.Context, slug string, in IssueOwnershipRules) (IssueOwnershipRules, error) {
	var out IssueOwnershipRules
	err := c.do(ctx, http.MethodPut, issueAppPath(slug)+"/issue-ownership-rules", in, &out)
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

// PreviewIssueSplit evaluates retained evidence without changing grouping.
func (c *Client) PreviewIssueSplit(ctx context.Context, slug, id string, in IssueSplitMatcher) (IssueSplitPreview, error) {
	var out IssueSplitPreview
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issues/"+url.PathEscape(id)+"/split-preview", in, &out)
	return out, err
}

func (c *Client) CreateIssueSplitRule(ctx context.Context, slug, source string, matcher IssueSplitMatcher) (IssueSplitRule, error) {
	var out IssueSplitRule
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issues/"+url.PathEscape(source)+"/split-rules", matcher, &out)
	return out, err
}
func (c *Client) UpdateIssueSplitRule(ctx context.Context, slug, source, id string, in IssueSplitRuleUpdate) (IssueSplitRule, error) {
	var out IssueSplitRule
	err := c.do(ctx, "PATCH", issueAppPath(slug)+"/issues/"+url.PathEscape(source)+"/split-rules/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) UndoIssueSplitRule(ctx context.Context, slug, source, id string, in IssueSplitRuleUndoRequest) (IssueSplitRule, error) {
	var out IssueSplitRule
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issues/"+url.PathEscape(source)+"/split-rules/"+url.PathEscape(id)+"/undo", in, &out)
	return out, err
}

// ListIssuesByTag matches retained occurrences in the fixed 24-hour window.
// The cursor must come from the same exact operational tag query.
func (c *Client) ListIssuesByTag(ctx context.Context, slug, tag, cursor string) (ListIssuesResponse, error) {
	q := url.Values{}
	q.Set("tag", tag)
	q.Set("cursor", cursor)
	var out ListIssuesResponse
	err := c.do(ctx, http.MethodGet, issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) CompareIssueReleases(ctx context.Context, slug, baseline, candidate, environment string) (IssueReleaseComparison, error) {
	q := url.Values{"baseline": {baseline}, "candidate": {candidate}, "environment": {environment}}
	var out IssueReleaseComparison
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-release-comparison?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) BulkActOnIssues(ctx context.Context, slug string, in BulkIssueActionRequest) (BulkIssueActionResponse, error) {
	var out BulkIssueActionResponse
	err := c.do(ctx, "POST", issueAppPath(slug)+"/issues/bulk-actions", in, &out)
	return out, err
}

func issueSavedViewPath(slug, key string) string {
	return issueAppPath(slug) + "/issue-saved-views/" + url.PathEscape(key)
}
func (c *Client) ListIssueSavedViews(ctx context.Context, slug string) (ListIssueSavedViewsResponse, error) {
	var out ListIssueSavedViewsResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-saved-views", nil, &out)
	return out, err
}
func (c *Client) GetIssueSavedView(ctx context.Context, slug, key string) (IssueSavedView, error) {
	var out IssueSavedView
	err := c.do(ctx, "GET", issueSavedViewPath(slug, key), nil, &out)
	return out, err
}
func (c *Client) PutIssueSavedView(ctx context.Context, slug, key string, in PutIssueSavedViewRequest) (IssueSavedView, error) {
	var out IssueSavedView
	err := c.do(ctx, "PUT", issueSavedViewPath(slug, key), in, &out)
	return out, err
}
func (c *Client) DeleteIssueSavedView(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", issueSavedViewPath(slug, key), nil, nil)
}
func (c *Client) ListIssuesForSavedView(ctx context.Context, slug, key, cursor string) (ListIssuesResponse, error) {
	view, err := c.GetIssueSavedView(ctx, slug, key)
	if err != nil {
		return ListIssuesResponse{}, err
	}
	q := view.Filters.Query()
	q.Set("cursor", cursor)
	var out ListIssuesResponse
	err = c.do(ctx, "GET", issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) ListIssueTeams(ctx context.Context, slug string) (ListIssueTeamsResponse, error) {
	var out ListIssueTeamsResponse
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-teams", nil, &out)
	return out, err
}
func (c *Client) GetIssueTeam(ctx context.Context, slug, key string) (IssueTeam, error) {
	var out IssueTeam
	err := c.do(ctx, "GET", issueAppPath(slug)+"/issue-teams/"+url.PathEscape(key), nil, &out)
	return out, err
}
func (c *Client) PutIssueTeam(ctx context.Context, slug, key string, in PutIssueTeamRequest) (IssueTeam, error) {
	var out IssueTeam
	err := c.do(ctx, "PUT", issueAppPath(slug)+"/issue-teams/"+url.PathEscape(key), in, &out)
	return out, err
}
func (c *Client) DeleteIssueTeam(ctx context.Context, slug, key string) error {
	return c.do(ctx, "DELETE", issueAppPath(slug)+"/issue-teams/"+url.PathEscape(key), nil, nil)
}
func (c *Client) ListIssuesForTeam(ctx context.Context, slug, key, cursor string) (ListIssuesResponse, error) {
	team, err := c.GetIssueTeam(ctx, slug, key)
	if err != nil {
		return ListIssuesResponse{}, err
	}
	q := url.Values{"team": {team.ID}, "state": {"open"}, "cursor": {cursor}}
	var out ListIssuesResponse
	err = c.do(ctx, "GET", issueAppPath(slug)+"/issues?"+q.Encode(), nil, &out)
	return out, err
}

func issueTrackerLinksPath(slug, id string) string {
	return issueAppPath(slug) + "/issues/" + url.PathEscape(id) + "/tracker-links"
}
func (c *Client) ListIssueTrackerLinks(ctx context.Context, slug, id, cursor string) (ListIssueTrackerLinksResponse, error) {
	var out ListIssueTrackerLinksResponse
	err := c.do(ctx, "GET", issueTrackerLinksPath(slug, id)+"?"+url.Values{"cursor": {cursor}}.Encode(), nil, &out)
	return out, err
}
func (c *Client) PutIssueTrackerLink(ctx context.Context, slug, id string, in PutIssueTrackerLinkRequest) (IssueTrackerLink, error) {
	var out IssueTrackerLink
	err := c.do(ctx, "POST", issueTrackerLinksPath(slug, id), in, &out)
	return out, err
}
func (c *Client) DeleteIssueTrackerLink(ctx context.Context, slug, id, link string) error {
	return c.do(ctx, "DELETE", issueTrackerLinksPath(slug, id)+"/"+url.PathEscape(link), nil, nil)
}

func (c *Client) GetNativeIssueTrackerStatus(ctx context.Context, slug, id, link string) (NativeIssueTrackerStatus, error) {
	var out NativeIssueTrackerStatus
	err := c.do(ctx, "GET", issueTrackerLinksPath(slug, id)+"/"+url.PathEscape(link)+"/native-status", nil, &out)
	return out, err
}
func (c *Client) UpdateNativeIssueTrackerStatus(ctx context.Context, slug, id, link string, in UpdateNativeIssueTrackerStatusRequest) (NativeIssueTrackerStatus, error) {
	var out NativeIssueTrackerStatus
	err := c.do(ctx, "POST", issueTrackerLinksPath(slug, id)+"/"+url.PathEscape(link)+"/native-status", in, &out)
	return out, err
}

// CreateNativeIssueTicket creates or recovers a GitHub ticket using the owned app connection.
func (c *PreviewClient) CreateNativeIssueTicket(ctx context.Context, slug, issueID string, in CreateNativeIssueTicketRequest) (NativeIssueTicketResult, error) {
	var out NativeIssueTicketResult
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/issues/"+url.PathEscape(issueID)+"/native-tickets", in, &out)
	return out, err
}

func (c *Client) GetIssueTrackerAutoSync(ctx context.Context, slug, issue, link string) (IssueTrackerAutoSync, error) {
	var out IssueTrackerAutoSync
	err := c.do(ctx, "GET", issueTrackerLinksPath(slug, issue)+"/"+url.PathEscape(link)+"/auto-sync", nil, &out)
	return out, err
}
func (c *Client) UpdateIssueTrackerAutoSync(ctx context.Context, slug, issue, link string, in UpdateIssueTrackerAutoSyncRequest) (IssueTrackerAutoSync, error) {
	var out IssueTrackerAutoSync
	err := c.do(ctx, "PUT", issueTrackerLinksPath(slug, issue)+"/"+url.PathEscape(link)+"/auto-sync", in, &out)
	return out, err
}

func (c *PreviewClient) GetIssueTrackerLinearConnection(ctx context.Context, slug string) (IssueTrackerLinearConnection, error) {
	var out IssueTrackerLinearConnection
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/issue-tracker-linear-connection", nil, &out)
	return out, err
}
func (c *PreviewClient) UpdateIssueTrackerLinearConnection(ctx context.Context, slug string, in UpdateIssueTrackerLinearConnectionRequest) (IssueTrackerLinearConnection, error) {
	var out IssueTrackerLinearConnection
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/issue-tracker-linear-connection", in, &out)
	return out, err
}

func (c *PreviewClient) CreateNativeLinearIssueTicket(ctx context.Context, slug, issue string, in CreateNativeLinearIssueTicketRequest) (NativeIssueTicketResult, error) {
	var out NativeIssueTicketResult
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/issues/"+url.PathEscape(issue)+"/native-linear-tickets", in, &out)
	return out, err
}

func (c *Client) GetNativeLinearIssueTrackerStatus(ctx context.Context, slug, issue, link string) (NativeLinearIssueTrackerStatus, error) {
	var out NativeLinearIssueTrackerStatus
	err := c.do(ctx, "GET", issueTrackerLinksPath(slug, issue)+"/"+url.PathEscape(link)+"/native-linear-status", nil, &out)
	return out, err
}
func (c *Client) UpdateNativeLinearIssueTrackerStatus(ctx context.Context, slug, issue, link string, in UpdateNativeLinearIssueTrackerStatusRequest) (NativeLinearIssueTrackerStatus, error) {
	var out NativeLinearIssueTrackerStatus
	err := c.do(ctx, "POST", issueTrackerLinksPath(slug, issue)+"/"+url.PathEscape(link)+"/native-linear-status", in, &out)
	return out, err
}
