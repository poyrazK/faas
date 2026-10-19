package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// AutomationLiveTest is deliberately separate from simulated expectations:
// real step states and retry counts describe execution, never mocked outcomes.
type AutomationLiveTest struct {
	ExpectedRunStatus string                      `json:"expected_run_status,omitempty"`
	Expectations      []AutomationLiveExpectation `json:"expectations"`
}

type AutomationLiveExpectation struct {
	Step         string          `json:"step"`
	Loop         string          `json:"loop,omitempty"`
	ItemIndex    *int            `json:"item_index,omitempty"`
	State        string          `json:"state,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	AttemptCount *int            `json:"attempt_count,omitempty"`
}

type StartAutomationLiveVerificationRequest struct {
	ExpectedVersion          int64  `json:"expected_version"`
	ExpectedPublishedVersion int64  `json:"expected_published_version"`
	ExpectedDeploymentID     string `json:"expected_deployment_id"`
	CaseName                 string `json:"case_name"`
	ConfirmRealActions       bool   `json:"confirm_real_actions"`
}

// AutomationLiveVerification exposes identities and safe summaries. Frozen
// assertions and actual step outputs are never returned by these endpoints.
type AutomationLiveVerification struct {
	RunID            string                `json:"run_id"`
	CaseName         string                `json:"case_name"`
	DeploymentID     string                `json:"deployment_id"`
	InputsHash       string                `json:"inputs_hash"`
	RunStatus        string                `json:"run_status"`
	Version          int64                 `json:"version"`
	PublishedVersion int64                 `json:"published_version"`
	CreatedAt        time.Time             `json:"created_at"`
	Outdated         bool                  `json:"outdated"`
	ReportCurrent    bool                  `json:"report_current"`
	Report           *AutomationLiveReport `json:"report,omitempty"`
}

type AutomationLiveReport struct {
	CheckedAt    time.Time                  `json:"checked_at"`
	Passed       bool                       `json:"passed"`
	Issue        string                     `json:"issue,omitempty"`
	RunStatus    string                     `json:"run_status"`
	ResumeCount  int                        `json:"resume_count"`
	RunUpdatedAt time.Time                  `json:"run_updated_at"`
	Steps        []AutomationTestStepResult `json:"steps"`
}

type ListAutomationLiveVerificationsResponse struct {
	DeploymentID     string                       `json:"deployment_id,omitempty"`
	PublishedVersion int64                        `json:"published_version"`
	Verifications    []AutomationLiveVerification `json:"verifications"`
}

func automationLivePath(slug, name string) string {
	return automationPath(slug) + "/" + url.PathEscape(name) + "/live-verifications"
}

func (c *Client) ListAutomationLiveVerifications(ctx context.Context, slug, name string) (ListAutomationLiveVerificationsResponse, error) {
	var out ListAutomationLiveVerificationsResponse
	err := c.do(ctx, http.MethodGet, automationLivePath(slug, name), nil, &out)
	return out, err
}

func (c *Client) StartAutomationLiveVerification(ctx context.Context, slug, name string, body StartAutomationLiveVerificationRequest, idempotencyKey string) (AutomationLiveVerification, error) {
	var out AutomationLiveVerification
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, automationLivePath(slug, name), body, &out, idempotencyKey)
	return out, err
}

func (c *Client) GetAutomationLiveVerification(ctx context.Context, slug, name, runID string) (AutomationLiveVerification, error) {
	var out AutomationLiveVerification
	err := c.do(ctx, http.MethodGet, automationLivePath(slug, name)+"/"+url.PathEscape(runID), nil, &out)
	return out, err
}

func (c *Client) CheckAutomationLiveVerification(ctx context.Context, slug, name, runID string) (AutomationLiveVerification, error) {
	var out AutomationLiveVerification
	err := c.do(ctx, http.MethodPost, automationLivePath(slug, name)+"/"+url.PathEscape(runID)+"/check", struct{}{}, &out)
	return out, err
}
