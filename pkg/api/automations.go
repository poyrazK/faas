package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AutomationResponse struct {
	FailurePaused    bool          `json:"failure_paused"`
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

// AutomationRevisionResponse is an immutable published automation snapshot.
// DefinitionHash is SHA-256 over the canonical JSON encoding of Definition.
type AutomationRevisionResponse struct {
	CheckEvidence        *AutomationCheckEvidence `json:"check_evidence,omitempty"`
	Version              int64                    `json:"version"`
	Definition           WorkflowSpec             `json:"definition"`
	DefinitionHash       string                   `json:"definition_hash"`
	RecordedAt           time.Time                `json:"recorded_at"`
	LegacySnapshot       bool                     `json:"legacy_snapshot"`
	PublishedByAccountID string                   `json:"published_by_account_id"`
	PublishedByAPIKeyID  string                   `json:"published_by_api_key_id,omitempty"`
}

type ListAutomationRevisionsResponse struct {
	Revisions []AutomationRevisionResponse `json:"revisions"`
	Total     int                          `json:"total"`
	Limit     int                          `json:"limit"`
	Offset    int                          `json:"offset"`
}

// AutomationHealthRun is the safe summary of a recent run returned by the
// automation health endpoint. Inputs, outputs and error text are omitted.
type AutomationHealthRun struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// AutomationHealthStepFailure counts terminal runs that failed at a logical
// workflow step. For each loop, all per-item failures are grouped by parent.
type AutomationHealthStepFailure struct {
	StepName       string    `json:"step_name"`
	FailedRunCount int64     `json:"failed_run_count"`
	LastFailedAt   time.Time `json:"last_failed_at"`
}

const (
	AutomationQueueReady            = "ready"
	AutomationQueueScheduled        = "scheduled"
	AutomationQueueRetryBackoff     = "retry_backoff"
	AutomationQueueParkedWait       = "parked_wait"
	AutomationQueueAppCapacity      = "app_capacity"
	AutomationQueueTenantCapacity   = "tenant_capacity"
	AutomationQueueWorkflowCapacity = "workflow_capacity"
)

// AutomationQueueHealth is a current admission snapshot. It contains neither
// customer payloads nor tenant/run identities, and does not estimate worker occupancy.
type AutomationQueueHealth struct {
	ObservedAt          time.Time        `json:"observed_at"`
	WaitingRunCount     int64            `json:"waiting_run_count"`
	DueRunCount         int64            `json:"due_run_count"`
	StaleRunCount       int64            `json:"stale_run_count"`
	OldestDueAgeSeconds float64          `json:"oldest_due_age_seconds"`
	AppRunningCount     int64            `json:"app_running_count"`
	AppDispatchLimit    int              `json:"app_dispatch_limit"`
	TenantDispatchLimit int              `json:"tenant_dispatch_limit"`
	AppAtCapacity       bool             `json:"app_at_capacity"`
	ReasonCounts        map[string]int64 `json:"reason_counts"`
}

// AutomationHealthResponse contains bounded operational aggregates, never
// customer run payloads or error strings.
type AutomationHealthResponse struct {
	AppSlug           string                        `json:"app_slug"`
	AutomationName    string                        `json:"automation_name"`
	WindowStart       time.Time                     `json:"window_start"`
	WindowEnd         time.Time                     `json:"window_end"`
	RunCount          int64                         `json:"run_count"`
	CompletedRunCount int64                         `json:"completed_run_count"`
	ActiveRunCount    int64                         `json:"active_run_count"`
	QueuedRunCount    int64                         `json:"queued_run_count"`
	Queue             *AutomationQueueHealth        `json:"queue,omitempty"`
	SuccessRate       float64                       `json:"success_rate"`
	StatusCounts      map[string]int64              `json:"status_counts"`
	P50DurationMS     *int64                        `json:"p50_duration_ms,omitempty"`
	P95DurationMS     *int64                        `json:"p95_duration_ms,omitempty"`
	LastRun           *AutomationHealthRun          `json:"last_run,omitempty"`
	LastSuccess       *AutomationHealthRun          `json:"last_success,omitempty"`
	LastFailure       *AutomationHealthRun          `json:"last_failure,omitempty"`
	FailedSteps       []AutomationHealthStepFailure `json:"failed_steps"`
}

type AutomationHealthOptions struct {
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type RestoreAutomationRevisionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type SaveAutomationDraftRequest struct {
	ExpectedVersion int64        `json:"expected_version"`
	Definition      WorkflowSpec `json:"definition"`
}

type PublishAutomationRequest struct {
	CheckReceipt     string                   `json:"check_receipt,omitempty"`
	CheckEvidence    *AutomationCheckEvidence `json:"check_evidence,omitempty"`
	ExpectedVersion  int64                    `json:"expected_version"`
	TakeOverManifest bool                     `json:"take_over_manifest,omitempty"`
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

func (c *Client) GetAutomationHealth(ctx context.Context, slug, name string, opts AutomationHealthOptions) (AutomationHealthResponse, error) {
	var out AutomationHealthResponse
	path := automationPath(slug) + "/" + url.PathEscape(name) + "/health"
	query := url.Values{}
	if opts.CreatedAfter != nil {
		query.Set("created_after", opts.CreatedAfter.UTC().Format(time.RFC3339Nano))
	}
	if opts.CreatedBefore != nil {
		query.Set("created_before", opts.CreatedBefore.UTC().Format(time.RFC3339Nano))
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}

func (c *Client) ListAutomationRevisions(ctx context.Context, slug, name string, limit, offset int) (ListAutomationRevisionsResponse, error) {
	var out ListAutomationRevisionsResponse
	path := automationPath(slug) + "/" + url.PathEscape(name) + "/revisions"
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
func (c *Client) GetAutomationRevision(ctx context.Context, slug, name string, version int64) (AutomationRevisionResponse, error) {
	var out AutomationRevisionResponse
	path := automationPath(slug) + "/" + url.PathEscape(name) + "/revisions/" + strconv.FormatInt(version, 10)
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
func (c *Client) RestoreAutomationRevision(ctx context.Context, slug, name string, version int64, body RestoreAutomationRevisionRequest) (AutomationResponse, error) {
	var out AutomationResponse
	path := automationPath(slug) + "/" + url.PathEscape(name) + "/revisions/" + strconv.FormatInt(version, 10) + "/restore"
	err := c.do(ctx, "POST", path, body, &out)
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
