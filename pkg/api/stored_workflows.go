package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type StoredWorkflowResponse struct {
	FailurePaused    bool                      `json:"failure_paused"`
	Name             string                    `json:"name"`
	Version          int64                     `json:"version"`
	Source           string                    `json:"source"`
	Draft            WorkflowSpec              `json:"draft"`
	Published        *WorkflowSpec             `json:"published,omitempty"`
	PublishedVersion int64                     `json:"published_version,omitempty"`
	Enabled          bool                      `json:"enabled"`
	UpdatedAt        *time.Time                `json:"updated_at,omitempty"`
	TestSuite        *StoredWorkflowTestSuite  `json:"test_suite,omitempty"`
	TestReport       *StoredWorkflowTestReport `json:"test_report,omitempty"`
}

type ListStoredWorkflowsResponse struct {
	AppSlug           string                   `json:"app_slug"`
	RuntimeEnabled    bool                     `json:"runtime_enabled"`
	UnavailableReason string                   `json:"unavailable_reason,omitempty"`
	MaxDefinitions    int                      `json:"max_definitions"`
	StoredWorkflows   []StoredWorkflowResponse `json:"stored_workflows"`
}

// StoredWorkflowRevisionResponse is an immutable published stored workflow snapshot.
// DefinitionHash is SHA-256 over the canonical JSON encoding of Definition.
type StoredWorkflowRevisionResponse struct {
	CheckEvidence        *StoredWorkflowCheckEvidence `json:"check_evidence,omitempty"`
	Version              int64                        `json:"version"`
	Definition           WorkflowSpec                 `json:"definition"`
	DefinitionHash       string                       `json:"definition_hash"`
	RecordedAt           time.Time                    `json:"recorded_at"`
	LegacySnapshot       bool                         `json:"legacy_snapshot"`
	PublishedByAccountID string                       `json:"published_by_account_id"`
	PublishedByAPIKeyID  string                       `json:"published_by_api_key_id,omitempty"`
}

type ListStoredWorkflowRevisionsResponse struct {
	Revisions []StoredWorkflowRevisionResponse `json:"revisions"`
	Total     int                              `json:"total"`
	Limit     int                              `json:"limit"`
	Offset    int                              `json:"offset"`
}

// StoredWorkflowHealthRun is the safe summary of a recent run returned by the
// stored workflow health endpoint. Inputs, outputs and error text are omitted.
type StoredWorkflowHealthRun struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// StoredWorkflowHealthStepFailure counts terminal runs that failed at a logical
// workflow step. For each loop, all per-item failures are grouped by parent.
type StoredWorkflowHealthStepFailure struct {
	StepName       string    `json:"step_name"`
	FailedRunCount int64     `json:"failed_run_count"`
	LastFailedAt   time.Time `json:"last_failed_at"`
}

const (
	StoredWorkflowQueueReady            = "ready"
	StoredWorkflowQueueScheduled        = "scheduled"
	StoredWorkflowQueueRetryBackoff     = "retry_backoff"
	StoredWorkflowQueueParkedWait       = "parked_wait"
	StoredWorkflowQueueAppCapacity      = "app_capacity"
	StoredWorkflowQueueTenantCapacity   = "tenant_capacity"
	StoredWorkflowQueueWorkflowCapacity = "workflow_capacity"
)

// StoredWorkflowQueueHealth is a current admission snapshot. It contains neither
// customer payloads nor tenant/run identities, and does not estimate worker occupancy.
type StoredWorkflowQueueHealth struct {
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

// StoredWorkflowHealthResponse contains bounded operational aggregates, never
// customer run payloads or error strings.
type StoredWorkflowHealthResponse struct {
	AppSlug            string                            `json:"app_slug"`
	StoredWorkflowName string                            `json:"stored_workflow_name"`
	WindowStart        time.Time                         `json:"window_start"`
	WindowEnd          time.Time                         `json:"window_end"`
	RunCount           int64                             `json:"run_count"`
	CompletedRunCount  int64                             `json:"completed_run_count"`
	ActiveRunCount     int64                             `json:"active_run_count"`
	QueuedRunCount     int64                             `json:"queued_run_count"`
	Queue              *StoredWorkflowQueueHealth        `json:"queue,omitempty"`
	SuccessRate        float64                           `json:"success_rate"`
	StatusCounts       map[string]int64                  `json:"status_counts"`
	P50DurationMS      *int64                            `json:"p50_duration_ms,omitempty"`
	P95DurationMS      *int64                            `json:"p95_duration_ms,omitempty"`
	LastRun            *StoredWorkflowHealthRun          `json:"last_run,omitempty"`
	LastSuccess        *StoredWorkflowHealthRun          `json:"last_success,omitempty"`
	LastFailure        *StoredWorkflowHealthRun          `json:"last_failure,omitempty"`
	FailedSteps        []StoredWorkflowHealthStepFailure `json:"failed_steps"`
}

type StoredWorkflowHealthOptions struct {
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type RestoreStoredWorkflowRevisionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type SaveStoredWorkflowDraftRequest struct {
	ExpectedVersion int64        `json:"expected_version"`
	Definition      WorkflowSpec `json:"definition"`
}

type PublishStoredWorkflowRequest struct {
	CheckReceipt     string                       `json:"check_receipt,omitempty"`
	CheckEvidence    *StoredWorkflowCheckEvidence `json:"check_evidence,omitempty"`
	ExpectedVersion  int64                        `json:"expected_version"`
	TakeOverManifest bool                         `json:"take_over_manifest,omitempty"`
}

type SetStoredWorkflowEnabledRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	Enabled         *bool `json:"enabled"`
}

type ValidateStoredWorkflowRequest struct {
	Definition WorkflowSpec `json:"definition"`
}

type ValidateStoredWorkflowResponse struct {
	Valid      bool     `json:"valid"`
	Issues     []string `json:"issues"`
	StepOrder  []string `json:"step_order"`
	NextFireAt string   `json:"next_fire_at,omitempty"`
}

func storedWorkflowPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/workflows/stored"
}
func (c *Client) ListStoredWorkflows(ctx context.Context, slug string) (ListStoredWorkflowsResponse, error) {
	var out ListStoredWorkflowsResponse
	err := c.do(ctx, "GET", storedWorkflowPath(slug), nil, &out)
	return out, err
}
func (c *Client) GetStoredWorkflow(ctx context.Context, slug, name string) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	err := c.do(ctx, "GET", storedWorkflowPath(slug)+"/"+url.PathEscape(name), nil, &out)
	return out, err
}

func (c *Client) GetStoredWorkflowHealth(ctx context.Context, slug, name string, opts StoredWorkflowHealthOptions) (StoredWorkflowHealthResponse, error) {
	var out StoredWorkflowHealthResponse
	path := storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/health"
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

func (c *Client) ListStoredWorkflowRevisions(ctx context.Context, slug, name string, limit, offset int) (ListStoredWorkflowRevisionsResponse, error) {
	var out ListStoredWorkflowRevisionsResponse
	path := storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/revisions"
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
func (c *Client) GetStoredWorkflowRevision(ctx context.Context, slug, name string, version int64) (StoredWorkflowRevisionResponse, error) {
	var out StoredWorkflowRevisionResponse
	path := storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/revisions/" + strconv.FormatInt(version, 10)
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
func (c *Client) RestoreStoredWorkflowRevision(ctx context.Context, slug, name string, version int64, body RestoreStoredWorkflowRevisionRequest) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	path := storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/revisions/" + strconv.FormatInt(version, 10) + "/restore"
	err := c.do(ctx, "POST", path, body, &out)
	return out, err
}
func (c *Client) SaveStoredWorkflowDraft(ctx context.Context, slug, name string, body SaveStoredWorkflowDraftRequest) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	err := c.do(ctx, "PUT", storedWorkflowPath(slug)+"/"+url.PathEscape(name), body, &out)
	return out, err
}
func (c *Client) PublishStoredWorkflow(ctx context.Context, slug, name string, body PublishStoredWorkflowRequest) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	err := c.do(ctx, "POST", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/publish", body, &out)
	return out, err
}
func (c *Client) SetStoredWorkflowEnabled(ctx context.Context, slug, name string, body SetStoredWorkflowEnabledRequest) (StoredWorkflowResponse, error) {
	var out StoredWorkflowResponse
	err := c.do(ctx, "PUT", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+"/enabled", body, &out)
	return out, err
}
func (c *Client) ValidateStoredWorkflow(ctx context.Context, slug string, body ValidateStoredWorkflowRequest) (ValidateStoredWorkflowResponse, error) {
	var out ValidateStoredWorkflowResponse
	err := c.do(ctx, "POST", storedWorkflowPath(slug)+":validate", body, &out)
	return out, err
}
func (c *Client) DeleteStoredWorkflow(ctx context.Context, slug, name string, version int64, restoreManifest bool) error {
	return c.do(ctx, "DELETE", storedWorkflowPath(slug)+"/"+url.PathEscape(name)+fmt.Sprintf("?expected_version=%d&restore_manifest=%t", version, restoreManifest), nil, nil)
}

// ErrStoredWorkflowDefinitionsQuota shares the workflow quota code but reports
// definition counts rather than the unrelated concurrent-run limit.
func ErrStoredWorkflowDefinitionsQuota(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanWorkflowsQuota, "Stored workflow definition quota exceeded", fmt.Sprintf("stored workflow definitions exceed the %s plan limit (%d/%d), including YAML definitions and saved drafts", plan, observed, limit)).WithLimit(int64(limit), int64(observed))
}
