package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type AutomationResponse struct {
	Name             string        `json:"name"`
	Version          int64         `json:"version"`
	Source           string        `json:"source"`
	Draft            WorkflowSpec  `json:"draft"`
	Published        *WorkflowSpec `json:"published,omitempty"`
	PublishedVersion int64         `json:"published_version,omitempty"`
	Enabled          bool          `json:"enabled"`
	UpdatedAt        *time.Time    `json:"updated_at,omitempty"`
}

type ListAutomationsResponse struct {
	AppSlug           string               `json:"app_slug"`
	RuntimeEnabled    bool                 `json:"runtime_enabled"`
	UnavailableReason string               `json:"unavailable_reason,omitempty"`
	MaxDefinitions    int                  `json:"max_definitions"`
	Automations       []AutomationResponse `json:"automations"`
}

type SaveAutomationDraftRequest struct {
	ExpectedVersion int64        `json:"expected_version"`
	Definition      WorkflowSpec `json:"definition"`
}

type PublishAutomationRequest struct {
	ExpectedVersion  int64 `json:"expected_version"`
	TakeOverManifest bool  `json:"take_over_manifest,omitempty"`
}

type SetAutomationEnabledRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	Enabled         *bool `json:"enabled"`
}

type ValidateAutomationRequest struct {
	Definition WorkflowSpec `json:"definition"`
}

type ValidateAutomationResponse struct {
	Valid      bool     `json:"valid"`
	Issues     []string `json:"issues"`
	StepOrder  []string `json:"step_order"`
	NextFireAt string   `json:"next_fire_at,omitempty"`
}

func automationPath(slug string) string { return "/v1/apps/" + url.PathEscape(slug) + "/automations" }
func (c *Client) ListAutomations(ctx context.Context, slug string) (ListAutomationsResponse, error) {
	var out ListAutomationsResponse
	err := c.do(ctx, "GET", automationPath(slug), nil, &out)
	return out, err
}
func (c *Client) GetAutomation(ctx context.Context, slug, name string) (AutomationResponse, error) {
	var out AutomationResponse
	err := c.do(ctx, "GET", automationPath(slug)+"/"+url.PathEscape(name), nil, &out)
	return out, err
}
func (c *Client) SaveAutomationDraft(ctx context.Context, slug, name string, body SaveAutomationDraftRequest) (AutomationResponse, error) {
	var out AutomationResponse
	err := c.do(ctx, "PUT", automationPath(slug)+"/"+url.PathEscape(name), body, &out)
	return out, err
}
func (c *Client) PublishAutomation(ctx context.Context, slug, name string, body PublishAutomationRequest) (AutomationResponse, error) {
	var out AutomationResponse
	err := c.do(ctx, "POST", automationPath(slug)+"/"+url.PathEscape(name)+"/publish", body, &out)
	return out, err
}
func (c *Client) SetAutomationEnabled(ctx context.Context, slug, name string, body SetAutomationEnabledRequest) (AutomationResponse, error) {
	var out AutomationResponse
	err := c.do(ctx, "PUT", automationPath(slug)+"/"+url.PathEscape(name)+"/enabled", body, &out)
	return out, err
}
func (c *Client) ValidateAutomation(ctx context.Context, slug string, body ValidateAutomationRequest) (ValidateAutomationResponse, error) {
	var out ValidateAutomationResponse
	err := c.do(ctx, "POST", automationPath(slug)+":validate", body, &out)
	return out, err
}
func (c *Client) DeleteAutomation(ctx context.Context, slug, name string, version int64, restoreManifest bool) error {
	return c.do(ctx, "DELETE", automationPath(slug)+"/"+url.PathEscape(name)+fmt.Sprintf("?expected_version=%d&restore_manifest=%t", version, restoreManifest), nil, nil)
}

// ErrAutomationDefinitionsQuota shares the workflow quota code but reports
// definition counts rather than the unrelated concurrent-run limit.
func ErrAutomationDefinitionsQuota(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanWorkflowsQuota, "Automation definition quota exceeded", fmt.Sprintf("automation definitions exceed the %s plan limit (%d/%d), including YAML definitions and saved drafts", plan, observed, limit)).WithLimit(int64(limit), int64(observed))
}
