package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	ProjectRollbackMaxStabilizeSeconds          = 60
	ProjectRollbackPreparing                    = "preparing"
	ProjectRollbackCutover                      = "cutover"
	ProjectRollbackReleasing                    = "releasing"
	CodeProjectRollbackPrepared                 = "project_rollback_prepared"
	CodeProjectRollbackPreparationExpired       = "project_rollback_preparation_expired"
	CodeProjectRollbackPreparationReleased      = "project_rollback_preparation_released"
	CodeProjectRollbackProbeRequired            = "project_rollback_health_probe_required"
	CodeProjectRollbackHealthFailed             = "project_rollback_health_failed"
	CodeProjectRollbackHealthTimeout            = "project_rollback_health_timeout"
	CodeProjectRollbackLiveTrafficRequired      = "project_rollback_live_traffic_required"
	CodeProjectRollbackLiveTrafficFailed        = "project_rollback_live_traffic_failed"
	CodeProjectRollbackLiveTrafficTimeout       = "project_rollback_live_traffic_timeout"
	CodeProjectRollbackPreCutoverHealthFailed   = "project_rollback_pre_cutover_health_failed"
	CodeProjectRollbackPreCutoverHealthTimeout  = "project_rollback_pre_cutover_health_timeout"
	CodeProjectRollbackUndoUnavailable          = "project_rollback_undo_unavailable"
	CodeProjectRollbackUndoStarted              = "project_rollback_undo_started"
	CodeProjectRollbackPaused                   = "project_rollback_paused"
	CodeProjectRollbackConflict                 = "project_rollback_conflict"
	CodeProjectRollbackRequestReused            = "project_rollback_request_reused"
	CodeProjectRollbackPlanChanged              = "project_rollback_plan_changed"
	CodeProjectRollbackUndoArtifactsUnavailable = "project_rollback_undo_artifacts_unavailable"
	CodeProjectRollbackAutoUndoBlocked          = "project_rollback_auto_undo_blocked"
	ProjectRollbackAutoUndoLiveTrafficTrigger   = "live_traffic"
	ProjectRollbackAutoUndoPending              = "pending"
	ProjectRollbackAutoUndoReady                = "ready"
	ProjectRollbackAutoUndoStarted              = "started"
	ProjectRollbackAutoUndoBlocked              = "blocked"
)

// ProjectRollbackHistoryLimit bounds the recent project rollback list.
const ProjectRollbackHistoryLimit = 20
const ProjectRollbackTargetsLimit = 20
const ProjectRollbackProtectionMaxSeconds = 7 * 24 * 60 * 60

type ProjectRollbackTargets struct {
	Project   string                        `json:"project"`
	ProjectID string                        `json:"project_id"`
	Deploys   []ProjectRollbackTargetDeploy `json:"deploys"`
}

type ProjectRollbackTargetDeploy struct {
	DeployGroupID string                     `json:"deploy_group_id"`
	CreatedAt     time.Time                  `json:"created_at"`
	ServiceCount  int                        `json:"service_count"`
	Protection    *ProjectRollbackProtection `json:"protection,omitempty"`
}

type ProtectProjectRollbackDeployRequest struct {
	TTLSeconds int `json:"ttl_seconds"`
}

type ProjectRollbackProtection struct {
	DeployGroupID string    `json:"deploy_group_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	LayerBytes    int64     `json:"layer_bytes"`
}

func (c *Client) ProtectProjectRollbackDeploy(ctx context.Context, slug, deploy string, req ProtectProjectRollbackDeployRequest) (ProjectRollbackProtection, error) {
	var out ProjectRollbackProtection
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug)+"/targets/"+url.PathEscape(deploy)+"/protection", req, &out)
	return out, err
}

type ProjectRollbackHistory struct {
	Project    string                   `json:"project"`
	ProjectID  string                   `json:"project_id"`
	Operations []ProjectRollbackSummary `json:"operations"`
}

type ProjectRollbackSummary struct {
	ID                string     `json:"operation_id"`
	Status            string     `json:"status"`
	Phase             string     `json:"phase,omitempty"`
	CutoversCompleted int        `json:"cutovers_completed"`
	CutoversTotal     int        `json:"cutovers_total"`
	UndoOf            string     `json:"undo_of,omitempty"`
	UndoOperationID   string     `json:"undo_operation_id,omitempty"`
	PauseRequestedAt  *time.Time `json:"pause_requested_at,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

type ProjectRollbackRequest struct {
	All                      bool   `json:"all,omitempty"`
	AcknowledgeReleases      bool   `json:"acknowledge_releases,omitempty"`
	Verify                   bool   `json:"verify,omitempty"`
	VerifyBeforeCutover      bool   `json:"verify_before_cutover,omitempty"`
	StabilizeForSeconds      int    `json:"stabilize_for_seconds,omitempty"`
	VerifyLiveTraffic        bool   `json:"verify_live_traffic,omitempty"`
	UndoOnHealthFailure      bool   `json:"undo_on_health_failure,omitempty"`
	UndoOnLiveTrafficFailure bool   `json:"undo_on_live_traffic_failure,omitempty"`
	RequestID                string `json:"request_id,omitempty"`
	ExpectedPlanHash         string `json:"expected_plan_hash,omitempty"`
	ToDeploy                 string `json:"to_deploy,omitempty"`
}

func ValidProjectRollbackPlanHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ProjectRollbackUndoRequest carries release acknowledgement and an optional preview hash.
type ProjectRollbackUndoRequest struct {
	AcknowledgeReleases bool   `json:"acknowledge_releases,omitempty"`
	ExpectedPlanHash    string `json:"expected_plan_hash,omitempty"`
}

type ProjectRollbackResumeRequest struct {
	ExpectedPlanHash string `json:"expected_plan_hash,omitempty"`
}

// ProjectRollbackOperation records ordered, exact service rollbacks for one project.
type ProjectRollbackOperation struct {
	ID                       string                   `json:"operation_id,omitempty"`
	Project                  string                   `json:"project"`
	ProjectID                string                   `json:"project_id"`
	RequestID                string                   `json:"request_id,omitempty"`
	PlanHash                 string                   `json:"plan_hash,omitempty"`
	ResumePlanHash           string                   `json:"resume_plan_hash,omitempty"`
	UndoOf                   string                   `json:"undo_of,omitempty"`
	UndoOperationID          string                   `json:"undo_operation_id,omitempty"`
	DeployGroupID            string                   `json:"deploy_group_id,omitempty"`
	AcknowledgeReleases      bool                     `json:"acknowledge_releases,omitempty"`
	Verify                   bool                     `json:"verify,omitempty"`
	VerifyBeforeCutover      bool                     `json:"verify_before_cutover,omitempty"`
	StabilizeForSeconds      int                      `json:"stabilize_for_seconds,omitempty"`
	VerifyLiveTraffic        bool                     `json:"verify_live_traffic,omitempty"`
	UndoOnHealthFailure      bool                     `json:"undo_on_health_failure,omitempty"`
	UndoOnLiveTrafficFailure bool                     `json:"undo_on_live_traffic_failure,omitempty"`
	AutoUndo                 *ProjectRollbackAutoUndo `json:"auto_undo,omitempty"`
	Status                   string                   `json:"status"`
	Phase                    string                   `json:"phase,omitempty"`
	PauseRequestedAt         *time.Time               `json:"pause_requested_at,omitempty"`
	PreparationDeadline      *time.Time               `json:"preparation_deadline,omitempty"`
	Steps                    []ProjectRollbackStep    `json:"steps"`
	CreatedAt                *time.Time               `json:"created_at,omitempty"`
	UpdatedAt                *time.Time               `json:"updated_at,omitempty"`
	RecoveryMS               *int64                   `json:"recovery_ms,omitempty"`
}

type ProjectRollbackAutoUndo struct {
	Status          string     `json:"status"`
	TriggerAppID    string     `json:"trigger_app_id"`
	Trigger         string     `json:"trigger,omitempty"`
	RouteGeneration *int64     `json:"route_generation,omitempty"`
	WindowStart     *time.Time `json:"window_start,omitempty"`
	WindowEnd       *time.Time `json:"window_end,omitempty"`
	Problem         *Problem   `json:"problem,omitempty"`
}

// MatchesLiveTrafficFailure binds a saved undo trigger to the failed cutover.
func (u ProjectRollbackAutoUndo) MatchesLiveTrafficFailure(step ProjectRollbackStep) bool {
	gate := step.LiveTraffic
	return u.Trigger == ProjectRollbackAutoUndoLiveTrafficTrigger &&
		step.AppID == u.TriggerAppID && step.Status == "rolled_back" &&
		gate != nil && gate.Status == "failed" && gate.Reason == "sustained_budget_violation" &&
		u.RouteGeneration != nil && gate.RouteGeneration != nil && *gate.RouteGeneration == *u.RouteGeneration &&
		u.WindowStart != nil && u.WindowEnd != nil && u.WindowEnd.Sub(*u.WindowStart) == RouteHealthWindows*RouteHealthWindow
}

type ProjectRollbackStep struct {
	Service           string                           `json:"service"`
	Slug              string                           `json:"slug"`
	Status            string                           `json:"status"`
	From              int                              `json:"from_revision,omitempty"`
	To                int                              `json:"to_revision,omitempty"`
	Reason            string                           `json:"reason,omitempty"`
	AppID             string                           `json:"app_id,omitempty"`
	Scope             string                           `json:"scope,omitempty"`
	CurrentID         string                           `json:"expected_current_deployment_id,omitempty"`
	CurrentServedAt   *time.Time                       `json:"current_served_at,omitempty"`
	TargetID          string                           `json:"target_deployment_id,omitempty"`
	DeploymentID      string                           `json:"deployment_id,omitempty"`
	AttemptID         string                           `json:"attempt_id,omitempty"`
	ReceiptID         string                           `json:"rollback_operation_id,omitempty"`
	Warnings          []BindingCheckFinding            `json:"warnings,omitempty"`
	CrossedReleases   []RollbackCrossedRelease         `json:"crossed_releases,omitempty"`
	UndoArtifacts     *ProjectRollbackUndoArtifacts    `json:"undo_artifacts,omitempty"`
	Problem           *Problem                         `json:"problem,omitempty"`
	Traffic           *ProjectRollbackTraffic          `json:"traffic,omitempty"`
	LiveTraffic       *ProjectRollbackLiveTraffic      `json:"live_traffic,omitempty"`
	Health            *ProjectRollbackHealth           `json:"health,omitempty"`
	PreCutoverHealth  *ProjectRollbackPreCutoverHealth `json:"pre_cutover_health,omitempty"`
	PreparationStatus string                           `json:"preparation_status,omitempty"`
	Blockers          []BindingCheckFinding            `json:"blockers,omitempty"`
}

// ProjectRollbackUndoArtifacts describes the hold for the exact undo deployment.
type ProjectRollbackUndoArtifacts struct {
	DeploymentID string     `json:"deployment_id"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// ProjectRollbackPreCutoverHealth records probes of a prepared, zero-traffic target.
type ProjectRollbackPreCutoverHealth struct {
	Status    string     `json:"status"`
	AttemptID string     `json:"attempt_id,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

func (step ProjectRollbackStep) PreCutoverHealthPassed() bool {
	h := step.PreCutoverHealth
	return h != nil && h.Status == "passed" && h.CheckedAt != nil &&
		step.AttemptID != "" && h.AttemptID == step.AttemptID
}

// ProjectRollbackHealth records the configured HTTP health gate for a cutover.
type ProjectRollbackHealth struct {
	Status          string     `json:"status"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	HealthySince    *time.Time `json:"healthy_since,omitempty"`
	RouteGeneration *int64     `json:"route_generation,omitempty"`
}

// PassedFor reports whether the saved evidence satisfies the verification policy.
func (h *ProjectRollbackHealth) PassedFor(seconds int) bool {
	return h != nil && h.Status == "passed" && (seconds == 0 ||
		h.HealthySince != nil && h.CheckedAt != nil && h.CheckedAt.Sub(*h.HealthySince) >= time.Duration(seconds)*time.Second)
}

// ProjectRollbackLiveTraffic records the production 5xx budget gate for an exact route.
type ProjectRollbackLiveTraffic struct {
	Status          string     `json:"status"`
	Reason          string     `json:"reason,omitempty"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	ObserveAfter    *time.Time `json:"observe_after,omitempty"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	RouteGeneration *int64     `json:"route_generation,omitempty"`
}

// Passed reports whether the gate has durable evidence for a route generation.
func (t *ProjectRollbackLiveTraffic) Passed() bool {
	return t != nil && t.Status == "passed" && t.CheckedAt != nil && t.RouteGeneration != nil
}

// ProjectRollbackTraffic records independently observed HTTP recovery milestones.
type ProjectRollbackTraffic struct {
	Status             string     `json:"status"`
	RouteGeneration    *int64     `json:"route_generation,omitempty"`
	RoutingCommittedAt *time.Time `json:"routing_committed_at,omitempty"`
	GatewaysAppliedAt  *time.Time `json:"gateways_applied_at,omitempty"`
	FirstSuccessAt     *time.Time `json:"first_success_at,omitempty"`
}

func (c *Client) PreviewProjectRollback(ctx context.Context, slug string, req ProjectRollbackRequest) (ProjectRollbackOperation, error) {
	query := url.Values{"all": {strconv.FormatBool(req.All)}, "acknowledge_releases": {strconv.FormatBool(req.AcknowledgeReleases)}}
	if req.Verify {
		query.Set("verify", "true")
	}
	if req.VerifyBeforeCutover {
		query.Set("verify_before_cutover", "true")
	}
	if req.StabilizeForSeconds != 0 {
		query.Set("stabilize_for_seconds", strconv.Itoa(req.StabilizeForSeconds))
	}
	if req.VerifyLiveTraffic {
		query.Set("verify_live_traffic", "true")
	}
	if req.UndoOnLiveTrafficFailure {
		query.Set("undo_on_live_traffic_failure", "true")
	}
	if req.UndoOnHealthFailure {
		query.Set("undo_on_health_failure", "true")
	}
	if req.ToDeploy != "" {
		query.Set("to_deploy", req.ToDeploy)
	}
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodGet, projectRollbackPath(slug)+"/preview?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) ListProjectRollbackTargets(ctx context.Context, slug string) (ProjectRollbackTargets, error) {
	var out ProjectRollbackTargets
	err := c.do(ctx, http.MethodGet, projectRollbackPath(slug)+"/targets", nil, &out)
	return out, err
}

func (c *Client) StartProjectRollback(ctx context.Context, slug string, req ProjectRollbackRequest) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug), req, &out)
	return out, err
}

func (c *Client) ListProjectRollbacks(ctx context.Context, slug string) (ProjectRollbackHistory, error) {
	var out ProjectRollbackHistory
	err := c.do(ctx, http.MethodGet, projectRollbackPath(slug), nil, &out)
	return out, err
}

func (c *Client) GetProjectRollback(ctx context.Context, slug, id string) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodGet, projectRollbackPath(slug)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) ResumeProjectRollback(ctx context.Context, slug, id string) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug)+"/"+url.PathEscape(id)+"/resume", nil, &out)
	return out, err
}

func (c *Client) ResumeReviewedProjectRollback(ctx context.Context, slug, id string, req ProjectRollbackResumeRequest) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug)+"/"+url.PathEscape(id)+"/resume", req, &out)
	return out, err
}

func (c *Client) PreviewProjectRollbackResume(ctx context.Context, slug, id string) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodGet, projectRollbackPath(slug)+"/"+url.PathEscape(id)+"/resume/preview", nil, &out)
	return out, err
}

func (c *Client) PauseProjectRollback(ctx context.Context, slug, id string) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug)+"/"+url.PathEscape(id)+"/pause", nil, &out)
	return out, err
}

func (c *Client) PreviewProjectRollbackUndo(ctx context.Context, slug, id string, acknowledgeReleases bool) (ProjectRollbackOperation, error) {
	path := projectRollbackPath(slug) + "/" + url.PathEscape(id) + "/undo/preview"
	if acknowledgeReleases {
		path += "?acknowledge_releases=true"
	}
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) UndoProjectRollback(ctx context.Context, slug, id string, req ProjectRollbackUndoRequest) (ProjectRollbackOperation, error) {
	var out ProjectRollbackOperation
	err := c.do(ctx, http.MethodPost, projectRollbackPath(slug)+"/"+url.PathEscape(id)+"/undo", req, &out)
	return out, err
}

func projectRollbackPath(slug string) string {
	return "/v1/projects/" + url.PathEscape(slug) + "/rollbacks"
}
