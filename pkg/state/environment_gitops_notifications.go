package state

import (
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentGitNotificationBaseline struct {
	Status     string `json:"status"`
	Generation int64  `json:"generation"`
}

type environmentGitNotificationState map[string]environmentGitNotificationBaseline

func environmentGitReportNotificationStatus(status string, plan api.EnvironmentGitOpsPlan) string {
	if status == "failed" || plan.Hash == "" {
		return "unavailable"
	}
	if len(plan.BlockingReasons) > 0 || status == "blocked" || status == "partial" {
		return "blocked"
	}
	if plan.HasDrift() {
		return "drifted"
	}
	return "in_sync"
}

func environmentGitSourceNotificationStatus(code string) string {
	switch code {
	case "":
		return "verified"
	case "environment_git_definition_invalid", "environment_git_scope_mismatch", "environment_git_approval_not_qualified":
		return "blocked"
	default:
		return "unavailable"
	}
}

func prepareEnvironmentGitNotification(before environmentGitNotificationState, source EnvironmentGitSource, project, axis, status, runID, code string, plan api.EnvironmentGitOpsPlan, now time.Time) (environmentGitNotificationState, []byte, string, error) {
	next := environmentGitNotificationState{}
	for key, baseline := range before {
		next[key] = baseline
	}
	previous := before[axis]
	next[axis] = environmentGitNotificationBaseline{Status: status, Generation: source.Generation}
	if previous.Status == status && (axis == "source" || previous.Generation == source.Generation) {
		return next, nil, "", nil
	}
	// Establish a healthy baseline silently. The first failure is actionable;
	// a later healthy result is a recovery, including after process restart.
	if previous.Status == "" || (previous.Generation != source.Generation && previous.Status == status) {
		previous.Status = "unknown"
		if status == "verified" || status == "in_sync" {
			return next, nil, "", nil
		}
	}
	id := uuid.NewString()
	payload := api.EnvironmentGitOpsChangedWebhookPayload{Version: 1, TransitionID: id, SourceID: source.ID,
		Project: project, Environment: source.EnvironmentSlug, Axis: axis, PreviousStatus: previous.Status, Status: status,
		Generation: source.Generation, ObservedVersion: source.IntentVersion, ApprovedRevision: source.ApprovedRevisionID,
		CommitSHA: plan.CommitSHA, RunID: runID, ErrorCode: code, ObservedAt: now.UTC(),
		DashboardPath: "/dashboard/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(source.EnvironmentSlug) + "/gitops",
		Changes:       []api.EnvironmentGitOpsChange{}, BlockingReasons: []string{}}
	for _, change := range plan.Changes {
		if change.Action == "keep" || change.Action == "retain_unmanaged" {
			continue
		}
		if len(payload.Changes) == api.EnvironmentGitOpsNotificationDetailsMax {
			payload.DetailsTruncated = true
			break
		}
		change.Before, change.After = nil, nil
		payload.Changes = append(payload.Changes, change)
	}
	if len(plan.BlockingReasons) > 0 {
		payload.BlockingReasons = slices.Clone(plan.BlockingReasons[:min(len(plan.BlockingReasons), api.EnvironmentGitOpsNotificationDetailsMax)])
	}
	if len(plan.BlockingReasons) > len(payload.BlockingReasons) {
		payload.DetailsTruncated = true
	}
	body, err := json.Marshal(payload)
	return next, body, id, err
}
