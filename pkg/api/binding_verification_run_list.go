package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Summaries describe retained receipt state, without requalifying evidence.
type BindingVerificationRunSummary struct {
	ID           string    `json:"id"`
	App          string    `json:"app,omitempty"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	GraphRunID   string    `json:"graph_run_id,omitempty"`
	Project      string    `json:"project,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type BindingVerificationRunList struct {
	Runs       []BindingVerificationRunSummary `json:"runs"`
	ObservedAt time.Time                       `json:"observed_at"`
	NextCursor string                          `json:"next_cursor,omitempty"`
}
type BindingVerificationRunListOptions struct {
	Limit          int
	Cursor         string
	DeploymentID   string
	Scope          string
	Status         string
	IncludeMembers bool
}

func verificationRunListQuery(o BindingVerificationRunListOptions) string {
	q := url.Values{}
	if o.Limit != 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	if o.DeploymentID != "" {
		q.Set("deployment_id", o.DeploymentID)
	}
	if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	if o.Status != "" {
		q.Set("status", o.Status)
	}
	if o.IncludeMembers {
		q.Set("include_members", "true")
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
func (c *Client) ListBindingVerificationRuns(ctx context.Context, app string, o BindingVerificationRunListOptions) (BindingVerificationRunList, error) {
	var out BindingVerificationRunList
	err := c.do(ctx, http.MethodGet, bindingRunPath(app, "")+verificationRunListQuery(o), nil, &out)
	return out, err
}
func (c *Client) ListProjectBindingVerificationRuns(ctx context.Context, project, environment string, o BindingVerificationRunListOptions) (BindingVerificationRunList, error) {
	var out BindingVerificationRunList
	err := c.do(ctx, http.MethodGet, projectBindingRunPath(project, environment, "")+verificationRunListQuery(o), nil, &out)
	return out, err
}
