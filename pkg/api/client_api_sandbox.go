package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func releaseAPISandboxPath(slug, deploymentID string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deploymentID)
}

// Sandbox invite, report, and callback credentials are separate from the
// account token. Keep their requests on the configured API origin.
func (c *Client) doAPISandboxGuest(ctx context.Context, method, path, credential string, body, out any) error {
	if credential == "" {
		return errors.New("api: sandbox credential is required")
	}
	guest := *c
	guest.token = credential
	guest.cache = nil
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	guest.http = &client
	return guest.do(ctx, method, path, body, out)
}

// GetDebugAPISandboxScenarioDraft drafts an API sandbox scenario from a retained request.
func (c *Client) GetDebugAPISandboxScenarioDraft(ctx context.Context, slug, requestID string) (APISandboxScenarioDraft, error) {
	var out APISandboxScenarioDraft
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/debug/requests/"+url.PathEscape(requestID)+"/sandbox-scenario-draft", nil, &out)
	return out, err
}

// ListAPISandboxSuiteRuns lists recent regression suite runs for a release.
func (c *Client) ListAPISandboxSuiteRuns(ctx context.Context, deploymentID string) (APISandboxSuiteRunsResponse, error) {
	var out APISandboxSuiteRunsResponse
	err := c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-suite-runs", nil, &out)
	return out, err
}

// GetAPISandboxSuiteRun reads one suite run and its scenario results.
func (c *Client) GetAPISandboxSuiteRun(ctx context.Context, deploymentID, runID string) (APISandboxSuiteRunReport, error) {
	var out APISandboxSuiteRunReport
	err := c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-suite-runs/"+url.PathEscape(runID), nil, &out)
	return out, err
}

// CreateAPISandboxSuiteRun queues the app's regression suite for a held build and live baseline.
func (c *Client) CreateAPISandboxSuiteRun(ctx context.Context, deploymentID, runID string, in APISandboxSuiteRunRequest) (APISandboxSuiteRunReport, error) {
	var out APISandboxSuiteRunReport
	err := c.do(ctx, http.MethodPut, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-suite-runs/"+url.PathEscape(runID), in, &out)
	return out, err
}

// ListSandboxApprovalChecks lists sandbox checks for a release approval.
func (c *Client) ListSandboxApprovalChecks(ctx context.Context, deploymentID string) (APISandboxApprovalChecksResponse, error) {
	var out APISandboxApprovalChecksResponse
	err := c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-checks", nil, &out)
	return out, err
}

// GetSandboxApprovalCheck reads a sandbox approval check.
func (c *Client) GetSandboxApprovalCheck(ctx context.Context, deploymentID, checkID string) (APISandboxApprovalCheck, error) {
	var out APISandboxApprovalCheck
	err := c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-checks/"+url.PathEscape(checkID), nil, &out)
	return out, err
}

// CreateSandboxApprovalCheck compares the held build with the current serving release in two sandboxes.
func (c *Client) CreateSandboxApprovalCheck(ctx context.Context, deploymentID, checkID string, in APISandboxComparisonRequest) (APISandboxApprovalCheck, error) {
	var out APISandboxApprovalCheck
	err := c.do(ctx, http.MethodPut, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals/sandbox-checks/"+url.PathEscape(checkID), in, &out)
	return out, err
}

// ListAPISandboxComparisons lists recent sandbox release comparisons and previous eligible baselines.
func (c *Client) ListAPISandboxComparisons(ctx context.Context, slug, deploymentID string) (APISandboxComparisonsResponse, error) {
	var out APISandboxComparisonsResponse
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-comparisons", nil, &out)
	return out, err
}

// GetAPISandboxComparison reads the current comparison verdict and its two run receipts.
func (c *Client) GetAPISandboxComparison(ctx context.Context, slug, deploymentID, comparisonID string) (APISandboxComparisonReport, error) {
	var out APISandboxComparisonReport
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-comparisons/"+url.PathEscape(comparisonID), nil, &out)
	return out, err
}

// CreateAPISandboxComparison runs the same saved sandbox scenario on two retained releases.
func (c *Client) CreateAPISandboxComparison(ctx context.Context, slug, deploymentID, comparisonID string, in APISandboxComparisonRequest) (APISandboxComparisonReport, error) {
	var out APISandboxComparisonReport
	err := c.do(ctx, http.MethodPut, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-comparisons/"+url.PathEscape(comparisonID), in, &out)
	return out, err
}

// ListAPISandboxRuns lists the 25 most recent scenario runs for a release.
func (c *Client) ListAPISandboxRuns(ctx context.Context, slug, deploymentID string) (APISandboxRunsResponse, error) {
	var out APISandboxRunsResponse
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs", nil, &out)
	return out, err
}

// GetAPISandboxRun reads progress or the report for a scenario run.
func (c *Client) GetAPISandboxRun(ctx context.Context, slug, deploymentID, runID string) (APISandboxRunReport, error) {
	var out APISandboxRunReport
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID), nil, &out)
	return out, err
}

// CreateAPISandboxRun starts a scenario run in a fresh copy of the selected release.
func (c *Client) CreateAPISandboxRun(ctx context.Context, slug, deploymentID, runID string, in APISandboxRunRequest) (APISandboxRunReport, error) {
	var out APISandboxRunReport
	err := c.do(ctx, http.MethodPut, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID), in, &out)
	return out, err
}

// IssueAPISandboxRunInvite issues a one-time invitation for a partner scenario run.
func (c *Client) IssueAPISandboxRunInvite(ctx context.Context, slug, deploymentID, runID string) (APISandboxPartnerInviteResponse, error) {
	var out APISandboxPartnerInviteResponse
	err := c.do(ctx, http.MethodPost, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID)+"/invite", nil, &out)
	return out, err
}

// ShareAPISandboxRun issues a read-only token for a finished scenario run report.
func (c *Client) ShareAPISandboxRun(ctx context.Context, slug, deploymentID, runID string) (APISandboxRunShareResponse, error) {
	var out APISandboxRunShareResponse
	err := c.do(ctx, http.MethodPost, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID)+"/report-link", nil, &out)
	return out, err
}

// CreateAPISandboxReceiverInvite invites a partner to verify a webhook receiver.
func (c *Client) CreateAPISandboxReceiverInvite(ctx context.Context, slug, deploymentID, runID string, in APISandboxReceiverInviteRequest) (APISandboxReceiverInvite, error) {
	var out APISandboxReceiverInvite
	err := c.do(ctx, http.MethodPost, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-invite", in, &out)
	return out, err
}

// GetAPISandboxReceiverInvite reads the receiver setup status.
func (c *Client) GetAPISandboxReceiverInvite(ctx context.Context, slug, deploymentID, runID string) (APISandboxReceiverInvite, error) {
	var out APISandboxReceiverInvite
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-invite", nil, &out)
	return out, err
}

// GetAPISandboxReceiverProof reads the endpoint ownership proof.
func (c *Client) GetAPISandboxReceiverProof(ctx context.Context, runID, credential string) (APISandboxReceiverProof, error) {
	var out APISandboxReceiverProof
	err := c.doAPISandboxGuest(ctx, http.MethodGet, "/v1/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-invite", credential, nil, &out)
	return out, err
}

// VerifyAPISandboxReceiver verifies the partner receiver and redeems the invitation.
func (c *Client) VerifyAPISandboxReceiver(ctx context.Context, runID, credential string, in APISandboxReceiverURL) (APISandboxReceiverCredentials, error) {
	var out APISandboxReceiverCredentials
	err := c.doAPISandboxGuest(ctx, http.MethodPost, "/v1/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-invite", credential, in, &out)
	return out, err
}

// ReadyAPISandboxReceiver confirms the partner is ready to receive the callback.
func (c *Client) ReadyAPISandboxReceiver(ctx context.Context, runID, credential string) error {
	return c.doAPISandboxGuest(ctx, http.MethodPost, "/v1/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-ready", credential, nil, nil)
}

// RecordAPISandboxReceiverOutcome submits the processor outcome for the retried delivery.
func (c *Client) RecordAPISandboxReceiverOutcome(ctx context.Context, runID, credential string, in APISandboxReceiverOutcome) error {
	return c.doAPISandboxGuest(ctx, http.MethodPost, "/v1/api-sandbox-runs/"+url.PathEscape(runID)+"/receiver-outcome", credential, in, nil)
}

// ReadSharedAPISandboxRun reads a finished run using its report token.
func (c *Client) ReadSharedAPISandboxRun(ctx context.Context, runID, credential string) (APISandboxRunReport, error) {
	var out APISandboxRunReport
	err := c.doAPISandboxGuest(ctx, http.MethodGet, "/v1/api-sandbox-runs/"+url.PathEscape(runID)+"/report", credential, nil, &out)
	return out, err
}

// GetAPISandboxSuite reads the app's saved API regression suite.
func (c *Client) GetAPISandboxSuite(ctx context.Context, slug string) (APISandboxSuiteResponse, error) {
	var out APISandboxSuiteResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-suite", nil, &out)
	return out, err
}

// PutAPISandboxSuite saves the app's API regression suite.
func (c *Client) PutAPISandboxSuite(ctx context.Context, slug string, in APISandboxSuiteSaveRequest) (APISandboxSuiteResponse, error) {
	var out APISandboxSuiteResponse
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-suite", in, &out)
	return out, err
}

// ListAPISandboxLibraryScenarios lists reusable sandbox scenarios for this app.
func (c *Client) ListAPISandboxLibraryScenarios(ctx context.Context, slug string) (APISandboxScenarioLibraryResponse, error) {
	var out APISandboxScenarioLibraryResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-scenarios", nil, &out)
	return out, err
}

// GetAPISandboxLibraryScenario reads a reusable sandbox scenario and its private provenance.
func (c *Client) GetAPISandboxLibraryScenario(ctx context.Context, slug, scenarioID string) (APISandboxLibraryScenario, error) {
	var out APISandboxLibraryScenario
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-scenarios/"+url.PathEscape(scenarioID), nil, &out)
	return out, err
}

// PutAPISandboxLibraryScenario saves a reusable sandbox scenario.
func (c *Client) PutAPISandboxLibraryScenario(ctx context.Context, slug, scenarioID string, in APISandboxScenarioSaveRequest) (APISandboxLibraryScenario, error) {
	var out APISandboxLibraryScenario
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-scenarios/"+url.PathEscape(scenarioID), in, &out)
	return out, err
}

// DeleteAPISandboxLibraryScenario removes a scenario from the app's library.
func (c *Client) DeleteAPISandboxLibraryScenario(ctx context.Context, slug, scenarioID, expectedRevision string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/api-sandbox-scenarios/"+url.PathEscape(scenarioID)+"?expected_revision="+url.QueryEscape(expectedRevision), nil, nil)
}

// GetReleaseAPISandboxScenario reads the saved API sandbox scenario for a release.
func (c *Client) GetReleaseAPISandboxScenario(ctx context.Context, slug, deploymentID string) (APISandboxScenarioResponse, error) {
	var out APISandboxScenarioResponse
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-scenario", nil, &out)
	return out, err
}

// PutReleaseAPISandboxScenario saves the API sandbox scenario for a release.
func (c *Client) PutReleaseAPISandboxScenario(ctx context.Context, slug, deploymentID string, in APISandboxScenarioSaveRequest) (APISandboxScenarioResponse, error) {
	var out APISandboxScenarioResponse
	err := c.do(ctx, http.MethodPut, releaseAPISandboxPath(slug, deploymentID)+"/api-sandbox-scenario", in, &out)
	return out, err
}

// ListReleaseAPISandboxes lists shareable API sandboxes for a release.
func (c *Client) ListReleaseAPISandboxes(ctx context.Context, slug, deploymentID string) (APISandboxListResponse, error) {
	var out APISandboxListResponse
	err := c.do(ctx, http.MethodGet, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes", nil, &out)
	return out, err
}

// EnsureReleaseAPISandbox queues or retrieves an API sandbox.
func (c *Client) EnsureReleaseAPISandbox(ctx context.Context, slug, deploymentID, sandboxID string, in APISandboxCreateRequest) (APISandboxResponse, error) {
	var out APISandboxResponse
	err := c.do(ctx, http.MethodPut, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes/"+url.PathEscape(sandboxID), in, &out)
	return out, err
}

// EndReleaseAPISandbox ends an API sandbox.
func (c *Client) EndReleaseAPISandbox(ctx context.Context, slug, deploymentID, sandboxID string) error {
	return c.do(ctx, http.MethodDelete, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes/"+url.PathEscape(sandboxID), nil, nil)
}

// CreateReleaseAPISandboxInvite issues an API sandbox invitation.
func (c *Client) CreateReleaseAPISandboxInvite(ctx context.Context, slug, deploymentID, sandboxID string) (APISandboxInviteResponse, error) {
	var out APISandboxInviteResponse
	err := c.do(ctx, http.MethodPost, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes/"+url.PathEscape(sandboxID)+"/invites", nil, &out)
	return out, err
}

// IssueReleaseAPISandboxPartnerInvite issues a one-time invite for a private partner copy.
func (c *Client) IssueReleaseAPISandboxPartnerInvite(ctx context.Context, slug, deploymentID, sandboxID string) (APISandboxPartnerInviteResponse, error) {
	var out APISandboxPartnerInviteResponse
	err := c.do(ctx, http.MethodPost, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes/"+url.PathEscape(sandboxID)+"/partner/invite", nil, &out)
	return out, err
}

// RevokeReleaseAPISandboxPartner revokes this partner copy's invite and redeemed key.
func (c *Client) RevokeReleaseAPISandboxPartner(ctx context.Context, slug, deploymentID, sandboxID string) error {
	return c.do(ctx, http.MethodDelete, releaseAPISandboxPath(slug, deploymentID)+"/api-sandboxes/"+url.PathEscape(sandboxID)+"/partner", nil, nil)
}

// RedeemAPISandboxPartnerInvite exchanges a one-time partner invite for a sandbox key.
func (c *Client) RedeemAPISandboxPartnerInvite(ctx context.Context, sandboxID, credential string) (APISandboxInviteResponse, error) {
	var out APISandboxInviteResponse
	err := c.doAPISandboxGuest(ctx, http.MethodPost, "/v1/api-sandboxes/"+url.PathEscape(sandboxID)+"/redeem", credential, nil, &out)
	return out, err
}

// GetAPISandboxInvite reads an API sandbox invitation.
func (c *Client) GetAPISandboxInvite(ctx context.Context, sandboxID, credential string) (APISandboxResponse, error) {
	var out APISandboxResponse
	err := c.doAPISandboxGuest(ctx, http.MethodGet, "/v1/api-sandboxes/"+url.PathEscape(sandboxID)+"/invite", credential, nil, &out)
	return out, err
}
