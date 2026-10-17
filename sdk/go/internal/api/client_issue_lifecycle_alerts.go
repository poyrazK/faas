package api

import (
	"context"
	"net/http"
)

func (c *Client) GetIssueLifecycleAlertPolicy(ctx context.Context, slug string) (IssueLifecycleAlertPolicy, error) {
	var out IssueLifecycleAlertPolicy
	err := c.do(ctx, http.MethodGet, issueAppPath(slug)+"/issue-lifecycle-alert-policy", nil, &out)
	return out, err
}
func (c *Client) SetIssueLifecycleAlertPolicy(ctx context.Context, slug string, in UpdateIssueLifecycleAlertPolicyRequest) (IssueLifecycleAlertPolicy, error) {
	var out IssueLifecycleAlertPolicy
	err := c.do(ctx, http.MethodPut, issueAppPath(slug)+"/issue-lifecycle-alert-policy", in, &out)
	return out, err
}
func (c *Client) PreviewIssueLifecycleAlert(ctx context.Context, slug string, in PreviewIssueLifecycleAlertRequest) (IssueLifecycleAlertPreview, error) {
	var out IssueLifecycleAlertPreview
	err := c.do(ctx, http.MethodPost, issueAppPath(slug)+"/issue-lifecycle-alert-policy/preview", in, &out)
	return out, err
}
