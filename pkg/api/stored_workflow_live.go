package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// StoredWorkflowLiveTest is deliberately separate from simulated expectations:
// real step states and retry counts describe execution, never mocked outcomes.
type StoredWorkflowLiveTest struct {
	ExpectedRunStatus string                          `json:"expected_run_status,omitempty"`
	Expectations      []StoredWorkflowLiveExpectation `json:"expectations"`
}

type StoredWorkflowLiveExpectation struct {
	Step         string          `json:"step"`
	Loop         string          `json:"loop,omitempty"`
	ItemIndex    *int            `json:"item_index,omitempty"`
	State        string          `json:"state,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	AttemptCount *int            `json:"attempt_count,omitempty"`
}

type StartStoredWorkflowLiveVerificationRequest struct {
	ExpectedVersion          int64  `json:"expected_version"`
	ExpectedPublishedVersion int64  `json:"expected_published_version"`
	ExpectedDeploymentID     string `json:"expected_deployment_id"`
	CaseName                 string `json:"case_name"`
	ConfirmRealActions       bool   `json:"confirm_real_actions"`
}

// StoredWorkflowLiveVerification exposes identities and safe summaries. Frozen
// assertions and actual step outputs are never returned by these endpoints.
type StoredWorkflowLiveVerification struct {
	RunID            string                    `json:"run_id"`
	CaseName         string                    `json:"case_name"`
	DeploymentID     string                    `json:"deployment_id"`
	InputsHash       string                    `json:"inputs_hash"`
	RunStatus        string                    `json:"run_status"`
	Version          int64                     `json:"version"`
	PublishedVersion int64                     `json:"published_version"`
	CreatedAt        time.Time                 `json:"created_at"`
	Outdated         bool                      `json:"outdated"`
	ReportCurrent    bool                      `json:"report_current"`
	Report           *StoredWorkflowLiveReport `json:"report,omitempty"`
}

type StoredWorkflowLiveReport struct {
	CheckedAt    time.Time                      `json:"checked_at"`
	Passed       bool                           `json:"passed"`
	Issue        string                         `json:"issue,omitempty"`
	RunStatus    string                         `json:"run_status"`
	ResumeCount  int                            `json:"resume_count"`
	RunUpdatedAt time.Time                      `json:"run_updated_at"`
	Steps        []StoredWorkflowTestStepResult `json:"steps"`
}

type ListStoredWorkflowLiveVerificationsResponse struct {
	DeploymentID     string                           `json:"deployment_id,omitempty"`
	PublishedVersion int64                            `json:"published_version"`
	Verifications    []StoredWorkflowLiveVerification `json:"verifications"`
}

func storedWorkflowLivePath(slug, name string) string {
	return storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/live-verifications"
}

func (c *Client) ListStoredWorkflowLiveVerifications(ctx context.Context, slug, name string) (ListStoredWorkflowLiveVerificationsResponse, error) {
	var out ListStoredWorkflowLiveVerificationsResponse
	err := c.do(ctx, http.MethodGet, storedWorkflowLivePath(slug, name), nil, &out)
	return out, err
}

func (c *Client) StartStoredWorkflowLiveVerification(ctx context.Context, slug, name string, body StartStoredWorkflowLiveVerificationRequest, idempotencyKey string) (StoredWorkflowLiveVerification, error) {
	var out StoredWorkflowLiveVerification
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, storedWorkflowLivePath(slug, name), body, &out, idempotencyKey)
	return out, err
}

func (c *Client) GetStoredWorkflowLiveVerification(ctx context.Context, slug, name, runID string) (StoredWorkflowLiveVerification, error) {
	var out StoredWorkflowLiveVerification
	err := c.do(ctx, http.MethodGet, storedWorkflowLivePath(slug, name)+"/"+url.PathEscape(runID), nil, &out)
	return out, err
}

func (c *Client) CheckStoredWorkflowLiveVerification(ctx context.Context, slug, name, runID string) (StoredWorkflowLiveVerification, error) {
	var out StoredWorkflowLiveVerification
	err := c.do(ctx, http.MethodPost, storedWorkflowLivePath(slug, name)+"/"+url.PathEscape(runID)+"/check", struct{}{}, &out)
	return out, err
}
