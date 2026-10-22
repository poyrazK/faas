package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Scheduled release states (ADR-20261021152201).
const (
	ScheduledReleaseStatePending    = "scheduled"
	ScheduledReleaseStateStarted    = "started"
	ScheduledReleaseStateBlocked    = "blocked"
	ScheduledReleaseStateExpired    = "expired"
	ScheduledReleaseStateCancelled  = "cancelled"
	ScheduledReleaseStateSuperseded = "superseded"
)

// ScheduledRelease is a release built ahead of time that starts its rollout
// at ReleaseAt.
type ScheduledRelease struct {
	ID           string `json:"id"`
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	Revision     int    `json:"revision,omitempty"`
	ReleaseAt    string `json:"release_at"`
	// State is scheduled, started, blocked, expired, cancelled or superseded.
	State string `json:"state"`
	// Canary is the ladder the rollout starts with; absent, the release
	// takes all traffic at once.
	Canary *CanaryPresetSpec `json:"canary,omitempty"`
	// NotifyChannels are told when the release starts or cannot start.
	NotifyChannels []string `json:"notify_channels"`
	// OverrideFreeze is the reason given for releasing into the freezes in
	// OverriddenFreezes.
	OverrideFreeze    string   `json:"override_freeze,omitempty"`
	OverriddenFreezes []string `json:"overridden_freezes,omitempty"`
	// OutcomeCode and OutcomeDetail say why a schedule ended without
	// starting, such as release_frozen.
	OutcomeCode   string `json:"outcome_code,omitempty"`
	OutcomeDetail string `json:"outcome_detail,omitempty"`
	ScheduledBy   string `json:"scheduled_by"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	StartedAt     string `json:"started_at,omitempty"`
}

// ScheduledReleaseListResponse is the body of GET /v1/scheduled-releases.
type ScheduledReleaseListResponse struct {
	Items []ScheduledRelease `json:"items"`
}

// ScheduleReleaseRequest is the body of PUT /v1/deployments/{id}/schedule.
type ScheduleReleaseRequest struct {
	// ReleaseAt is an RFC 3339 time; a time that has passed starts the
	// release on apid's next pass.
	ReleaseAt string `json:"release_at"`
	// Canary is the ladder the rollout starts with. Absent, the release gets
	// the canary the default release policy gives a deploy now.
	Canary *CanaryPresetSpec `json:"canary,omitempty"`
	// NotifyChannels names Slack or email notification channels.
	NotifyChannels []string `json:"notify_channels,omitempty"`
	// OverrideFreeze releases into the freezes active at ReleaseAt, audited.
	OverrideFreeze string `json:"override_freeze,omitempty"`
}

// ParseReleaseAt validates a release time against now.
func ParseReleaseAt(field, value string, now time.Time) (time.Time, *Problem) {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, ErrValidation(field + " must be an RFC 3339 time, such as 2026-10-22T02:00:00Z")
	}
	if at.Before(now.Add(-ScheduledReleasePastSkew)) {
		return time.Time{}, ErrValidation(field + " has passed; schedule a time in the future")
	}
	if at.After(now.Add(ScheduledReleaseMaxAhead)) {
		return time.Time{}, ErrValidation(fmt.Sprintf("%s is more than %d days ahead", field, int(ScheduledReleaseMaxAhead.Hours()/24))).
			WithLimit(int64(ScheduledReleaseMaxAhead.Hours()), int64(at.Sub(now).Hours()))
	}
	return at.UTC(), nil
}

// ValidateScheduleNotify checks a list of notification channel names.
func ValidateScheduleNotify(field string, names []string) *Problem {
	if len(names) > ScheduledReleaseNotifyChannelsMax {
		return ErrValidation(fmt.Sprintf("%s names at most %d channels", field, ScheduledReleaseNotifyChannelsMax)).
			WithLimit(ScheduledReleaseNotifyChannelsMax, int64(len(names)))
	}
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			return ErrValidation(field + " needs distinct channel names")
		}
		seen[n] = true
	}
	return nil
}

// ErrReleaseScheduled refuses a traffic change on a release that waits for
// its scheduled start.
func ErrReleaseScheduled(release, at string) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseScheduled, "Release is scheduled",
		fmt.Sprintf("%s starts its rollout at %s. Cancel the schedule (gregale scheduled-releases cancel) or move it (gregale scheduled-releases set --at now) first.", release, at))
}

// ErrReleaseNotParked refuses to schedule a release that is not waiting
// without traffic.
func ErrReleaseNotParked(release, why string) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseNotParked, "Release cannot be scheduled",
		fmt.Sprintf("%s %s; only a release that is live without traffic can be scheduled.", release, why))
}

// ErrScheduledReleaseNotFound reports a release without a (pending) schedule.
func ErrScheduledReleaseNotFound(detail string) *Problem {
	return NewProblem(http.StatusNotFound, CodeScheduledReleaseNotFound, "No scheduled release", detail)
}

// ListScheduledReleases calls GET /v1/scheduled-releases; app narrows the
// list to one app.
func (c *Client) ListScheduledReleases(ctx context.Context, app string) ([]ScheduledRelease, error) {
	path := "/v1/scheduled-releases"
	if app != "" {
		path += "?app=" + url.QueryEscape(app)
	}
	var out ScheduledReleaseListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetDeploymentSchedule calls GET /v1/deployments/{id}/schedule.
func (c *Client) GetDeploymentSchedule(ctx context.Context, deploymentID string) (ScheduledRelease, error) {
	var out ScheduledRelease
	err := c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/schedule", nil, &out)
	return out, err
}

// ScheduleDeployment calls PUT /v1/deployments/{id}/schedule.
func (c *Client) ScheduleDeployment(ctx context.Context, deploymentID string, req ScheduleReleaseRequest) (ScheduledRelease, error) {
	var out ScheduledRelease
	err := c.do(ctx, http.MethodPut, "/v1/deployments/"+url.PathEscape(deploymentID)+"/schedule", req, &out)
	return out, err
}

// CancelDeploymentSchedule calls DELETE /v1/deployments/{id}/schedule.
func (c *Client) CancelDeploymentSchedule(ctx context.Context, deploymentID string) (ScheduledRelease, error) {
	var out ScheduledRelease
	err := c.do(ctx, http.MethodDelete, "/v1/deployments/"+url.PathEscape(deploymentID)+"/schedule", nil, &out)
	return out, err
}
