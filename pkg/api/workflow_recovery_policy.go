package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type WorkflowRecoveryResponsePlan struct {
	Version   int                              `json:"version"`
	App       string                           `json:"app"`
	AppID     string                           `json:"app_id"`
	JobID     string                           `json:"job_id"`
	Key       string                           `json:"key"`
	CreatedAt time.Time                        `json:"created_at"`
	Actions   []WorkflowRecoveryResponseAction `json:"actions"`
}
type WorkflowRecoveryResponseAction struct {
	Kind         string                               `json:"kind"`
	Position     int64                                `json:"position"`
	RunID        string                               `json:"run_id"`
	Cancellation *WorkflowRecoveryCancellationRequest `json:"cancellation,omitempty"`
	Retry        *EventRecoveryRequest                `json:"retry,omitempty"`
}

// WorkflowRecoveryPolicy prepares plans and optionally authorizes bounded automatic retries.
type WorkflowRecoveryPolicy struct {
	HealthAlerts          *WorkflowRecoveryHealthAlertPolicy `json:"health_alerts,omitempty"`
	HourlyExecutionBudget int                                `json:"hourly_execution_budget,omitempty"`
	PauseAfterFailures    int                                `json:"pause_after_failures,omitempty"`
	Enabled               bool                               `json:"enabled"`
	Mode                  string                             `json:"mode"`
	WorkflowNames         []string                           `json:"workflow_names"`
	Triggers              []string                           `json:"triggers"`
	FailureStatuses       []string                           `json:"failure_statuses"`
	MaxResumeCount        int                                `json:"max_resume_count"`
	CooldownSeconds       int                                `json:"cooldown_seconds"`
	HourlyActionBudget    int                                `json:"hourly_action_budget"`
	RatePerSecond         int                                `json:"rate_per_second"`
	Reason                string                             `json:"reason"`
}
type SetWorkflowRecoveryPolicyRequest struct {
	ExpectedVersion int64                  `json:"expected_version"`
	Policy          WorkflowRecoveryPolicy `json:"policy"`
}
type WorkflowRecoveryAutomaticPollingFailure struct {
	Code     string    `json:"code"`
	Attempts int64     `json:"attempts"`
	FailedAt time.Time `json:"failed_at"`
	RetryAt  time.Time `json:"retry_at"`
}

type WorkflowRecoveryPolicyAutomaticExecution struct {
	PollingFailure      *WorkflowRecoveryAutomaticPollingFailure `json:"polling_failure,omitempty"`
	ConsecutiveFailures int                                      `json:"consecutive_failures"`
	PausedAt            *time.Time                               `json:"paused_at,omitempty"`
	PauseReason         string                                   `json:"pause_reason"`
}

// LastEvaluation is worker evidence; queue reads never refresh it.
type WorkflowRecoveryAutomaticEvaluation struct {
	EvaluatedAt  time.Time  `json:"evaluated_at"`
	Reason       string     `json:"reason"`
	WaitingSince *time.Time `json:"waiting_since,omitempty"`
}
type WorkflowRecoveryAutomaticAction struct {
	LastEvaluation      *WorkflowRecoveryAutomaticEvaluation          `json:"last_evaluation,omitempty"`
	Suppression         *WorkflowRecoveryAutomaticSuppression         `json:"suppression,omitempty"`
	BlockedNotification *WorkflowRecoveryAutomaticBlockedNotification `json:"blocked_notification,omitempty"`
	ActionID            string                                        `json:"action_id"`
	Position            int64                                         `json:"position"`
	Status              string                                        `json:"status"`
	Reason              string                                        `json:"reason"`
	ChildJobID          string                                        `json:"child_job_id,omitempty"`
	AppliedAt           *time.Time                                    `json:"applied_at,omitempty"`
	Result              *WorkflowRecoveryReviewActionStatus           `json:"result,omitempty"`
}
type WorkflowRecoveryReviewAutomaticExecution struct {
	Actions []WorkflowRecoveryAutomaticAction `json:"actions"`
}
type WorkflowRecoveryPolicyPausedWebhookPayload struct {
	EventID             string    `json:"event_id"`
	AppID               string    `json:"app_id"`
	App                 string    `json:"app"`
	PolicyVersion       int64     `json:"policy_version"`
	PausedAt            time.Time `json:"paused_at"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Reason              string    `json:"reason"`
	PolicyURL           string    `json:"policy_url"`
}
type WorkflowRecoveryPolicyResponse struct {
	PollingNotification *WorkflowRecoveryPollingNotification      `json:"polling_notification,omitempty"`
	AutomaticExecution  *WorkflowRecoveryPolicyAutomaticExecution `json:"automatic_execution,omitempty"`
	AppID               string                                    `json:"app_id"`
	Version             int64                                     `json:"version"`
	Policy              WorkflowRecoveryPolicy                    `json:"policy"`
	UpdatedAt           time.Time                                 `json:"updated_at"`
	ActorKind           string                                    `json:"actor_kind"`
	ActorID             string                                    `json:"actor_id"`
	LastEvaluationAt    *time.Time                                `json:"last_evaluation_at,omitempty"`
	Status              string                                    `json:"status"`
}
type WorkflowRecoveryPolicyDecision struct {
	PreflightBlockers []string `json:"preflight_blockers,omitempty"`
	FailureStatus     string   `json:"failure_status,omitempty"`
	PreflightOutcome  string   `json:"preflight_outcome,omitempty"`
	Position          int64    `json:"position"`
	RunID             string   `json:"run_id"`
	ResumeCount       int      `json:"resume_count"`
	Trigger           string   `json:"trigger"`
	Action            string   `json:"action"`
	Outcome           string   `json:"outcome"`
}
type WorkflowRecoveryPolicyReview struct {
	ParentEvidenceRepairs  map[string]WorkflowInitialEvidenceRepairReceipt `json:"parent_evidence_repairs,omitempty"`
	EvidenceRepairs        map[string]WorkflowInitialEvidenceRepairReceipt `json:"evidence_repairs,omitempty"`
	AutomaticExecution     *WorkflowRecoveryReviewAutomaticExecution       `json:"automatic_execution,omitempty"`
	CompletionNotification *WorkflowRecoveryReviewCompletionNotification   `json:"completion_notification,omitempty"`
	Notification           *WorkflowRecoveryReviewNotification             `json:"notification,omitempty"`
	ReviewDecision         *WorkflowRecoveryReviewDecision                 `json:"review_decision,omitempty"`
	ID                     string                                          `json:"id"`
	AppID                  string                                          `json:"app_id"`
	JobID                  string                                          `json:"job_id"`
	PolicyVersion          int64                                           `json:"policy_version"`
	Policy                 WorkflowRecoveryPolicy                          `json:"policy"`
	ObservedAt             time.Time                                       `json:"observed_at"`
	Decisions              []WorkflowRecoveryPolicyDecision                `json:"decisions"`
	Plan                   *WorkflowRecoveryResponsePlan                   `json:"plan,omitempty"`
}
type WorkflowRecoveryPolicyReviews struct {
	Reviews   []WorkflowRecoveryPolicyReview `json:"reviews"`
	NextAfter string                         `json:"next_after,omitempty"`
}

func WorkflowRecoveryResponseActionID(p WorkflowRecoveryResponsePlan, a WorkflowRecoveryResponseAction) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("automation-response:%q:%q:%q:%q:%d", p.AppID, p.JobID, p.Key, a.Kind, a.Position))).String()
}
func (r SetWorkflowRecoveryPolicyRequest) Validate() error {
	p := r.Policy
	if p.HealthAlerts != nil {
		if err := p.HealthAlerts.Validate(); err != nil {
			return err
		}
	}
	if r.ExpectedVersion < 0 || (p.Mode != "preview" && !WorkflowRecoveryPolicyAutomaticMode(p.Mode)) || len(p.WorkflowNames) < 1 || len(p.WorkflowNames) > WorkflowRecoveryPolicyFiltersMax || len(p.Triggers) < 1 || len(p.Triggers) > 4 || len(p.FailureStatuses) > 2 || p.MaxResumeCount < 1 || p.MaxResumeCount > WorkflowRunMaxResumes || p.CooldownSeconds < WorkflowRecoveryPolicyMinCooldownSeconds || p.CooldownSeconds > WorkflowRecoveryPolicyMaxCooldownSeconds || p.HourlyActionBudget < 1 || p.HourlyActionBudget > WorkflowRecoveryPolicyHourlyActionsMax || p.RatePerSecond < 1 || p.RatePerSecond > EventRecoveryRateMax || strings.TrimSpace(p.Reason) == "" {
		return fmt.Errorf("invalid recovery policy filters or limits")
	}
	if WorkflowRecoveryPolicyAutomaticMode(p.Mode) {
		trigger := "saved_failure"
		if p.Mode == "automatic_initial" {
			trigger = "initial_failure"
		}
		if len(p.Triggers) != 1 || p.Triggers[0] != trigger || len(p.FailureStatuses) == 0 || p.HourlyExecutionBudget < 1 || p.HourlyExecutionBudget > WorkflowRecoveryPolicyHourlyActionsMax || p.PauseAfterFailures < 1 || p.PauseAfterFailures > WorkflowRecoveryPolicyPauseFailuresMax {
			return fmt.Errorf("automatic modes require their single matching trigger, explicit failure statuses, an execution budget and a failure pause limit")
		}
	} else if p.HourlyExecutionBudget != 0 || p.PauseAfterFailures != 0 {
		return fmt.Errorf("automatic execution limits require an automatic mode")
	}
	for _, trigger := range p.Triggers {
		if trigger == "initial_failure" && len(p.FailureStatuses) == 0 {
			return fmt.Errorf("initial_failure requires explicit failure statuses")
		}
	}
	seen := map[string]bool{}
	for _, name := range p.WorkflowNames {
		if strings.TrimSpace(name) != name || name == "" || len(name) > AutomationNameMaxBytes || strings.ContainsAny(name, "/\\\x00") || seen[name] {
			return fmt.Errorf("invalid or duplicate workflow filter")
		}
		seen[name] = true
	}
	seen = map[string]bool{}
	for _, trigger := range p.Triggers {
		if trigger != "initial_failure" && trigger != "saved_failure" && trigger != "stalled" && trigger != "deadline_exceeded" || seen[trigger] {
			return fmt.Errorf("invalid or duplicate recovery trigger")
		}
		seen[trigger] = true
	}
	seen = map[string]bool{}
	for _, status := range p.FailureStatuses {
		if status != "failed" && status != "dead" || seen[status] {
			return fmt.Errorf("invalid or duplicate failure status")
		}
		seen[status] = true
	}
	return (EventRecoveryControlRequest{Reason: p.Reason}).Validate()
}
func recoveryPolicyPath(app string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/recovery-policy"
}
func (c *Client) GetWorkflowRecoveryPolicy(ctx context.Context, app string) (WorkflowRecoveryPolicyResponse, error) {
	var out WorkflowRecoveryPolicyResponse
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app), nil, &out)
	return out, err
}
func (c *Client) SetWorkflowRecoveryPolicy(ctx context.Context, app string, req SetWorkflowRecoveryPolicyRequest) (WorkflowRecoveryPolicyResponse, error) {
	var out WorkflowRecoveryPolicyResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPut, recoveryPolicyPath(app), req, &out)
	return out, err
}
func (c *Client) ListWorkflowRecoveryPolicyReviews(ctx context.Context, app, after string, limit int) (WorkflowRecoveryPolicyReviews, error) {
	var out WorkflowRecoveryPolicyReviews
	v := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/reviews?"+v.Encode(), nil, &out)
	return out, err
}
func (c *Client) GetWorkflowRecoveryPolicyReview(ctx context.Context, app, id string) (WorkflowRecoveryPolicyReview, error) {
	var out WorkflowRecoveryPolicyReview
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/reviews/"+url.PathEscape(id), nil, &out)
	return out, err
}

// MarshalJSON keeps the empty failure filter an array in every policy snapshot.
func (p WorkflowRecoveryPolicy) MarshalJSON() ([]byte, error) {
	type wirePolicy WorkflowRecoveryPolicy
	if p.FailureStatuses == nil {
		p.FailureStatuses = []string{}
	}
	return json.Marshal(wirePolicy(p))
}

// Review decisions never authorize automatic application of the saved plan.
type WorkflowRecoveryReviewDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}
type WorkflowRecoveryReviewDecision struct {
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	DecidedAt time.Time `json:"decided_at"`
	ActorKind string    `json:"actor_kind"`
	ActorID   string    `json:"actor_id"`
}
type WorkflowRecoveryReviewStatus struct {
	Outcome    string                               `json:"outcome"`
	Complete   bool                                 `json:"complete"`
	ReviewID   string                               `json:"review_id"`
	ObservedAt time.Time                            `json:"observed_at"`
	Status     string                               `json:"status"`
	Actions    []WorkflowRecoveryReviewActionStatus `json:"actions"`
}
type WorkflowRecoveryReviewActionStatus struct {
	Outcome        string     `json:"outcome"`
	Complete       bool       `json:"complete"`
	RunID          string     `json:"run_id"`
	ResumeCount    int        `json:"resume_count"`
	RunURL         string     `json:"run_url"`
	ChildJobID     string     `json:"child_job_id,omitempty"`
	ItemsURL       string     `json:"items_url,omitempty"`
	EvidenceSource string     `json:"evidence_source,omitempty"`
	RecordedAt     *time.Time `json:"recorded_at,omitempty"`
	OutcomeReason  string     `json:"outcome_reason,omitempty"`
	Position       int64      `json:"position"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
}

func (r WorkflowRecoveryReviewDecisionRequest) Validate() error {
	if r.Decision != "approved" && r.Decision != "dismissed" {
		return fmt.Errorf("decision must be approved or dismissed")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("review reason is required")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}
func (c *Client) DecideWorkflowRecoveryPolicyReview(ctx context.Context, app, id string, req WorkflowRecoveryReviewDecisionRequest) (WorkflowRecoveryPolicyReview, error) {
	var out WorkflowRecoveryPolicyReview
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, recoveryPolicyPath(app)+"/reviews/"+url.PathEscape(id)+"/decision", req, &out)
	return out, err
}
func (c *Client) GetWorkflowRecoveryReviewStatus(ctx context.Context, app, id string) (WorkflowRecoveryReviewStatus, error) {
	var out WorkflowRecoveryReviewStatus
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/reviews/"+url.PathEscape(id)+"/status", nil, &out)
	return out, err
}

// Capture evidence is retained with the review; delivery outcomes use the webhook ledger.
type WorkflowRecoveryReviewNotification struct {
	EventID            string    `json:"event_id"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	CapturedAt         time.Time `json:"captured_at"`
	ReceiverWebhookIDs []string  `json:"receiver_webhook_ids"`
}
type WorkflowRecoveryReviewReadyWebhookPayload struct {
	InitialFailureID string                           `json:"initial_failure_id,omitempty"`
	EventID          string                           `json:"event_id"`
	ReviewID         string                           `json:"review_id"`
	AppID            string                           `json:"app_id"`
	App              string                           `json:"app"`
	JobID            string                           `json:"job_id,omitempty"`
	PolicyVersion    int64                            `json:"policy_version"`
	PreparedAt       time.Time                        `json:"prepared_at"`
	ObservedAt       time.Time                        `json:"observed_at"`
	Targets          []WorkflowRecoveryPolicyDecision `json:"targets"`
	ReviewURL        string                           `json:"review_url"`
	StatusURL        string                           `json:"status_url"`
	ReviewCommand    string                           `json:"review_command"`
	ExportCommand    string                           `json:"export_command"`
}

type WorkflowRecoveryReviewCompletionNotification struct {
	EventID            string                       `json:"event_id"`
	Status             string                       `json:"status"`
	CapturedAt         time.Time                    `json:"captured_at"`
	ReceiverWebhookIDs []string                     `json:"receiver_webhook_ids"`
	Result             WorkflowRecoveryReviewStatus `json:"result"`
}
type WorkflowRecoveryReviewFinishedWebhookPayload struct {
	EventID       string                       `json:"event_id"`
	ReviewID      string                       `json:"review_id"`
	AppID         string                       `json:"app_id"`
	App           string                       `json:"app"`
	JobID         string                       `json:"job_id"`
	PolicyVersion int64                        `json:"policy_version"`
	CapturedAt    time.Time                    `json:"captured_at"`
	Result        WorkflowRecoveryReviewStatus `json:"result"`
	ReviewURL     string                       `json:"review_url"`
	StatusURL     string                       `json:"status_url"`
	StatusCommand string                       `json:"status_command"`
}

// Operational controls preserve the policy version and its prepared queue.
type ControlWorkflowRecoveryPolicyRequest struct {
	ExpectedVersion   int64      `json:"expected_version"`
	ExpectedUpdatedAt time.Time  `json:"expected_updated_at"`
	ExpectedPausedAt  *time.Time `json:"expected_paused_at,omitempty"`
	Operation         string     `json:"operation"`
	Reason            string     `json:"reason"`
}

func (r ControlWorkflowRecoveryPolicyRequest) Validate() error {
	if r.ExpectedVersion < 1 || r.ExpectedUpdatedAt.IsZero() || (r.Operation != "pause" && r.Operation != "resume") || strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("control requires observed version, update time, pause time, operation and reason")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}

type WorkflowRecoveryQueueEvidence struct {
	Diagnosis   WorkflowRetryEvidenceDiagnosis `json:"diagnosis"`
	EvidenceURL string                         `json:"evidence_url"`
	CLICommand  string                         `json:"cli_command"`
}

type WorkflowRecoveryInitialQueueEvidence struct {
	Stage       string                           `json:"stage"`
	Diagnosis   WorkflowInitialEvidenceDiagnosis `json:"diagnosis"`
	EvidenceURL string                           `json:"evidence_url"`
	CLICommand  string                           `json:"cli_command"`
}

type WorkflowRecoveryAutomaticQueueEntry struct {
	InitialEvidence           *WorkflowRecoveryInitialQueueEvidence `json:"initial_evidence,omitempty"`
	Evidence                  *WorkflowRecoveryQueueEvidence        `json:"evidence,omitempty"`
	EvidenceUnavailableReason string                                `json:"evidence_unavailable_reason,omitempty"`
	WaitCategory              string                                `json:"wait_category,omitempty"`
	WaitReason                string                                `json:"wait_reason,omitempty"`
	QueuedAt                  time.Time                             `json:"queued_at"`
	QueuedAgeSeconds          int64                                 `json:"queued_age_seconds"`
	WaitingAgeSeconds         *int64                                `json:"waiting_age_seconds,omitempty"`
	ReviewID                  string                                `json:"review_id"`
	PolicyVersion             int64                                 `json:"policy_version"`
	Action                    WorkflowRecoveryAutomaticAction       `json:"action"`
}
type WorkflowRecoveryAutomaticQueue struct {
	HealthIncident   *WorkflowRecoveryHealthIncident       `json:"health_incident,omitempty"`
	Policy           WorkflowRecoveryPolicyResponse        `json:"policy"`
	ObservedAt       time.Time                             `json:"observed_at"`
	Pending          int                                   `json:"pending"`
	Outstanding      int                                   `json:"outstanding"`
	HourlyExecutions int                                   `json:"hourly_executions"`
	RemainingBudget  int                                   `json:"remaining_budget"`
	CooldownUntil    *time.Time                            `json:"cooldown_until,omitempty"`
	Entries          []WorkflowRecoveryAutomaticQueueEntry `json:"entries"`
	Truncated        bool                                  `json:"truncated"`
}

func (c *Client) ControlWorkflowRecoveryPolicy(ctx context.Context, app string, req ControlWorkflowRecoveryPolicyRequest) (WorkflowRecoveryPolicyResponse, error) {
	var out WorkflowRecoveryPolicyResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, recoveryPolicyPath(app)+"/control", req, &out)
	return out, err
}
func (c *Client) GetWorkflowRecoveryAutomaticQueue(ctx context.Context, app string) (WorkflowRecoveryAutomaticQueue, error) {
	var out WorkflowRecoveryAutomaticQueue
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/queue", nil, &out)
	return out, err
}

type SuppressWorkflowRecoveryAutomaticActionRequest struct {
	ExpectedPolicyVersion int64  `json:"expected_policy_version"`
	Reason                string `json:"reason"`
}

func (r SuppressWorkflowRecoveryAutomaticActionRequest) Validate() error {
	if r.ExpectedPolicyVersion < 1 || strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("suppression requires the observed policy version and a reason")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}

type WorkflowRecoveryAutomaticSuppression struct {
	Reason       string    `json:"reason"`
	ActorKind    string    `json:"actor_kind"`
	ActorID      string    `json:"actor_id"`
	SuppressedAt time.Time `json:"suppressed_at"`
}
type WorkflowRecoveryAutomaticBlockedNotification struct {
	State              string    `json:"state,omitempty"`
	Sequence           int64     `json:"sequence,omitempty"`
	EvidenceKind       string    `json:"evidence_kind,omitempty"`
	DiagnosisReason    string    `json:"diagnosis_reason,omitempty"`
	EventID            string    `json:"event_id"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	CapturedAt         time.Time `json:"captured_at"`
	ReceiverWebhookIDs []string  `json:"receiver_webhook_ids"`
}
type WorkflowRecoveryQueueBlockedWebhookPayload struct {
	EvidenceKind     string    `json:"evidence_kind,omitempty"`
	DiagnosisReason  string    `json:"diagnosis_reason,omitempty"`
	EvidenceURL      string    `json:"evidence_url,omitempty"`
	RunID            string    `json:"run_id,omitempty"`
	ResumeCount      *int64    `json:"resume_count,omitempty"`
	Repairable       *bool     `json:"repairable,omitempty"`
	CLICommand       string    `json:"cli_command,omitempty"`
	InitialFailureID string    `json:"initial_failure_id,omitempty"`
	State            string    `json:"state,omitempty"`
	PreviousReason   string    `json:"previous_reason,omitempty"`
	OperatorAction   string    `json:"operator_action,omitempty"`
	JobURL           string    `json:"job_url,omitempty"`
	EventID          string    `json:"event_id"`
	AppID            string    `json:"app_id"`
	App              string    `json:"app"`
	ReviewID         string    `json:"review_id"`
	ActionID         string    `json:"action_id"`
	PolicyVersion    int64     `json:"policy_version"`
	Position         int64     `json:"position"`
	ChildJobID       string    `json:"child_job_id,omitempty"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	ObservedAt       time.Time `json:"observed_at"`
	QueueURL         string    `json:"queue_url"`
	ReviewStatusURL  string    `json:"review_status_url"`
}

func (c *Client) SuppressWorkflowRecoveryAutomaticAction(ctx context.Context, app, review, action string, r SuppressWorkflowRecoveryAutomaticActionRequest) (WorkflowRecoveryAutomaticAction, error) {
	var out WorkflowRecoveryAutomaticAction
	if err := r.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, recoveryPolicyPath(app)+"/reviews/"+url.PathEscape(review)+"/actions/"+url.PathEscape(action)+"/suppress", r, &out)
	return out, err
}

// WorkflowRecoveryPolicyAutomaticMode identifies modes with explicit execution authority.
func WorkflowRecoveryPolicyAutomaticMode(mode string) bool {
	return mode == "automatic_retry" || mode == "automatic_initial"
}
