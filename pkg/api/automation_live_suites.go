package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// SaveAutomationLiveSuiteRequest groups existing verification runs. Saving a
// suite never admits, checks, resumes or cancels workflow execution.
type SaveAutomationLiveSuiteRequest struct {
	ExpectedVersion          int64                       `json:"expected_version"`
	ExpectedPublishedVersion int64                       `json:"expected_published_version"`
	ExpectedDeploymentID     string                      `json:"expected_deployment_id"`
	Cases                    []AutomationLiveSuiteMember `json:"cases"`
}

type AutomationLiveSuiteMember struct {
	Name  string `json:"name"`
	RunID string `json:"run_id,omitempty"`
}

type AutomationLiveSuiteSummary struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Version          int64     `json:"version"`
	PublishedVersion int64     `json:"published_version"`
	DeploymentID     string    `json:"deployment_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CaseCount        int       `json:"case_count"`
	LinkedRuns       int       `json:"linked_runs"`
	Managed          bool      `json:"managed,omitempty"`
}

type AutomationLiveSuiteCase struct {
	Name         string                      `json:"name"`
	RunID        string                      `json:"run_id,omitempty"`
	Outcome      string                      `json:"outcome"`
	Verification *AutomationLiveVerification `json:"verification,omitempty"`
}

type AutomationLiveSuiteCounts struct {
	Selected    int `json:"selected"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Pending     int `json:"pending"`
	NotStarted  int `json:"not_started"`
	Unavailable int `json:"unavailable"`
	Outdated    int `json:"outdated"`
}

type AutomationLiveSuite struct {
	AutomationLiveSuiteSummary
	Outcome    string                    `json:"outcome"`
	Outdated   bool                      `json:"outdated"`
	ObservedAt time.Time                 `json:"observed_at"`
	Summary    AutomationLiveSuiteCounts `json:"summary"`
	Cases      []AutomationLiveSuiteCase `json:"cases"`
}

type ListAutomationLiveSuitesResponse struct {
	Suites []AutomationLiveSuiteSummary `json:"suites"`
}

func automationLiveSuitesPath(slug, name string) string {
	return automationPath(slug) + "/" + url.PathEscape(name) + "/live-suites"
}

func (c *Client) SaveAutomationLiveSuite(ctx context.Context, slug, name, suiteID string, body SaveAutomationLiveSuiteRequest) (AutomationLiveSuite, error) {
	var out AutomationLiveSuite
	err := c.do(ctx, http.MethodPut, automationLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID), body, &out)
	return out, err
}

func (c *Client) GetAutomationLiveSuite(ctx context.Context, slug, name, suiteID string) (AutomationLiveSuite, error) {
	var out AutomationLiveSuite
	err := c.do(ctx, http.MethodGet, automationLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID), nil, &out)
	return out, err
}

func (c *Client) ListAutomationLiveSuites(ctx context.Context, slug, name string) (ListAutomationLiveSuitesResponse, error) {
	var out ListAutomationLiveSuitesResponse
	err := c.do(ctx, http.MethodGet, automationLiveSuitesPath(slug, name), nil, &out)
	return out, err
}
