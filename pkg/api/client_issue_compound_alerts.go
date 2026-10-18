package api

import (
	"context"
	"net/http"
)

func (c *Client) GetIssueCompoundAlertPolicy(ctx context.Context, slug string) (IssueCompoundAlertPolicy, error) {
	var out IssueCompoundAlertPolicy
	err := c.do(ctx, http.MethodGet, issueAppPath(slug)+"/issue-compound-alert-policy", nil, &out)
	return out, err
}
func (c *Client) SetIssueCompoundAlertPolicy(ctx context.Context, slug string, in UpdateIssueCompoundAlertPolicyRequest) (IssueCompoundAlertPolicy, error) {
	var out IssueCompoundAlertPolicy
	err := c.do(ctx, http.MethodPut, issueAppPath(slug)+"/issue-compound-alert-policy", in, &out)
	return out, err
}
func (c *Client) PreviewIssueCompoundAlert(ctx context.Context, slug string, in PreviewIssueCompoundAlertRequest) (IssueCompoundAlertPreview, error) {
	var out IssueCompoundAlertPreview
	err := c.do(ctx, http.MethodPost, issueAppPath(slug)+"/issue-compound-alert-policy/preview", in, &out)
	return out, err
}
