package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// SaveStoredWorkflowLiveSuiteRequest groups existing verification runs. Saving a
// suite never admits, checks, resumes or cancels workflow execution.
type SaveStoredWorkflowLiveSuiteRequest struct {
	ExpectedVersion          int64                           `json:"expected_version"`
	ExpectedPublishedVersion int64                           `json:"expected_published_version"`
	ExpectedDeploymentID     string                          `json:"expected_deployment_id"`
	Cases                    []StoredWorkflowLiveSuiteMember `json:"cases"`
}

type StoredWorkflowLiveSuiteMember struct {
	Name  string `json:"name"`
	RunID string `json:"run_id,omitempty"`
}

type StoredWorkflowLiveSuiteSummary struct {
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

type StoredWorkflowLiveSuiteCase struct {
	Name         string                          `json:"name"`
	RunID        string                          `json:"run_id,omitempty"`
	Outcome      string                          `json:"outcome"`
	Verification *StoredWorkflowLiveVerification `json:"verification,omitempty"`
}

type StoredWorkflowLiveSuiteCounts struct {
	Selected    int `json:"selected"`
	Passed      int `json:"passed"`
	Failed      int `json:"failed"`
	Pending     int `json:"pending"`
	NotStarted  int `json:"not_started"`
	Unavailable int `json:"unavailable"`
	Outdated    int `json:"outdated"`
}

type StoredWorkflowLiveSuite struct {
	StoredWorkflowLiveSuiteSummary
	Outcome    string                        `json:"outcome"`
	Outdated   bool                          `json:"outdated"`
	ObservedAt time.Time                     `json:"observed_at"`
	Summary    StoredWorkflowLiveSuiteCounts `json:"summary"`
	Cases      []StoredWorkflowLiveSuiteCase `json:"cases"`
}

type ListStoredWorkflowLiveSuitesResponse struct {
	Suites []StoredWorkflowLiveSuiteSummary `json:"suites"`
}

func storedWorkflowLiveSuitesPath(slug, name string) string {
	return storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/live-suites"
}

func (c *Client) SaveStoredWorkflowLiveSuite(ctx context.Context, slug, name, suiteID string, body SaveStoredWorkflowLiveSuiteRequest) (StoredWorkflowLiveSuite, error) {
	var out StoredWorkflowLiveSuite
	err := c.do(ctx, http.MethodPut, storedWorkflowLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID), body, &out)
	return out, err
}

func (c *Client) GetStoredWorkflowLiveSuite(ctx context.Context, slug, name, suiteID string) (StoredWorkflowLiveSuite, error) {
	var out StoredWorkflowLiveSuite
	err := c.do(ctx, http.MethodGet, storedWorkflowLiveSuitesPath(slug, name)+"/"+url.PathEscape(suiteID), nil, &out)
	return out, err
}

func (c *Client) ListStoredWorkflowLiveSuites(ctx context.Context, slug, name string) (ListStoredWorkflowLiveSuitesResponse, error) {
	var out ListStoredWorkflowLiveSuitesResponse
	err := c.do(ctx, http.MethodGet, storedWorkflowLiveSuitesPath(slug, name), nil, &out)
	return out, err
}
