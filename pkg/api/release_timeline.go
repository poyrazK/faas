package api

import (
	"context"
	"net/http"
	"net/url"
)

// Release timeline phases (ADR-20261022120744), in the order they are
// matched.
const (
	ReleasePhaseBuilding         = "building"
	ReleasePhaseFailed           = "failed"
	ReleasePhaseCancelled        = "cancelled"
	ReleasePhaseRolledBack       = "rolled_back"
	ReleasePhaseScheduled        = "scheduled"
	ReleasePhaseAwaitingApproval = "awaiting_approval"
	ReleasePhaseSuperseded       = "superseded"
	ReleasePhaseHeld             = "held"
	ReleasePhaseRollingOut       = "rolling_out"
	ReleasePhaseWatching         = "watching"
	ReleasePhaseReleased         = "released"
)

// Release timeline event kinds (ADR-20261022120744).
const (
	ReleaseEventCreated           = "created"
	ReleaseEventScheduled         = "scheduled"
	ReleaseEventScheduleStarted   = "schedule_started"
	ReleaseEventScheduleBlocked   = "schedule_blocked"
	ReleaseEventScheduleCancelled = "schedule_cancelled"
	ReleaseEventApprovalRequested = "approval_requested"
	ReleaseEventApproved          = "approved"
	ReleaseEventRejected          = "rejected"
	ReleaseEventApprovalExpired   = "approval_expired"
	ReleaseEventRolloutStarted    = "rollout_started"
	ReleaseEventAdvanced          = "advanced"
	ReleaseEventTrafficChanged    = "traffic_changed"
	ReleaseEventHeld              = "held"
	ReleaseEventCompleted         = "completed"
	ReleaseEventAborted           = "aborted"
	ReleaseEventRollbackRequested = "rollback_requested"
	ReleaseEventRolledBack        = "rolled_back"
	ReleaseEventWatchPassed       = "watch_passed"
	ReleaseEventSuperseded        = "superseded"
	ReleaseEventNote              = "note"
)

// ReleaseTimeline is GET /v1/deployments/{id}/timeline: one release's phase
// and what happened to it, oldest event first (ADR-20261022120744).
type ReleaseTimeline struct {
	DeploymentID string `json:"deployment_id"`
	App          string `json:"app"`
	Revision     int    `json:"revision,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Status       string `json:"status"`
	Phase        string `json:"phase"`
	Summary      string `json:"summary"`
	// TrafficPercent is the release's share of traffic now; CanaryStep is
	// zero-based, as on the deployment.
	TrafficPercent   int    `json:"traffic_percent"`
	CanaryStep       int    `json:"canary_step,omitempty"`
	CanaryTotalSteps int    `json:"canary_total_steps,omitempty"`
	CreatedAt        string `json:"created_at"`
	DeployedBy       string `json:"deployed_by,omitempty"`
	CommitSHA        string `json:"commit_sha,omitempty"`
	PRNumber         int    `json:"pr_number,omitempty"`
	Tag              string `json:"tag,omitempty"`
	Reason           string `json:"reason,omitempty"`
	// Stable is the release this one replaces, the target of an abort.
	StableDeploymentID string `json:"stable_deployment_id,omitempty"`
	StableRevision     int    `json:"stable_revision,omitempty"`
	// ReplacedBy is the newer release of the same scope that took over.
	ReplacedByDeploymentID string `json:"replaced_by_deployment_id,omitempty"`
	ReplacedByRevision     int    `json:"replaced_by_revision,omitempty"`
	// WatchUntil is when the post-release watch stops judging the release.
	WatchUntil string `json:"watch_until,omitempty"`
	// Approval is present when an approval policy gates this release.
	Approval *ReleaseApprovalStatus `json:"approval,omitempty"`
	// Schedule is the release's latest schedule, if it had one.
	Schedule *ScheduledRelease `json:"schedule,omitempty"`
	// Gates are the results of the newest decision that judged gates.
	Gates  []ReleaseGateResult    `json:"gates,omitempty"`
	Events []ReleaseTimelineEvent `json:"events"`
	// Truncated means older audit rows were left out.
	Truncated bool `json:"truncated,omitempty"`
}

// ReleaseTimelineEvent is one thing that happened to a release.
type ReleaseTimelineEvent struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Actor is a person's email, or "Gregale" for automatic steps.
	Actor          string `json:"actor,omitempty"`
	TrafficPercent *int   `json:"traffic_percent,omitempty"`
	// Repeats counts the further holds with the same rule this event
	// stands for.
	Repeats  int              `json:"repeats,omitempty"`
	Decision *RolloutDecision `json:"decision,omitempty"`
}

// GetReleaseTimeline reads GET /v1/deployments/{id}/timeline.
func (c *Client) GetReleaseTimeline(ctx context.Context, deploymentID string) (ReleaseTimeline, error) {
	var out ReleaseTimeline
	return out, c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/timeline", nil, &out)
}
