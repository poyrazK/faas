// adr: 20261017190813 — durable, evidence-bound rollout outcome notifications.
package state

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollout"
)

type environmentGitNotificationEvent struct {
	id   string
	body []byte
}

// Keep one bounded rollout baseline alongside the independent drift/source
// baselines. A terminal outcome is emitted at most once per approval generation,
// even if evidence disappears and returns. A failed rollout may later serve.
func prepareEnvironmentGitRolloutNotifications(before environmentGitNotificationState, source EnvironmentGitSource, revision EnvironmentDesiredRevision,
	project, runID string, evidence *EnvironmentWorkloadActivationEvidence, now time.Time) (environmentGitNotificationState, []environmentGitNotificationEvent, error) {
	next := environmentGitNotificationState{}
	for axis, baseline := range before {
		next[axis] = baseline
	}
	previous := before["rollout"]
	events := []environmentGitNotificationEvent{}
	active := !source.Detached && !source.Suspended && source.Spec.Mode == "enforce" && source.ApprovedRevisionID != ""
	changed := previous.RevisionID != source.ApprovedRevisionID || previous.Generation != source.Generation
	if previous.RevisionID != "" && (changed || !active) {
		if !previous.ServingNotified && !previous.FailedNotified && previous.Status != "superseded" {
			reason := "approved_revision_changed"
			if !active {
				reason = "reconciliation_inactive"
			}
			event, err := environmentGitRolloutEvent(source, project, "", previous, "superseded", reason, nil, now)
			if err != nil {
				return nil, nil, err
			}
			events = append(events, event)
		}
		delete(next, "rollout")
		previous = environmentGitNotificationBaseline{}
	}
	if !active {
		return next, events, nil
	}
	if revision.ID != source.ApprovedRevisionID || revision.SourceID != source.ID || !environmentCommitRE.MatchString(revision.CommitSHA) ||
		!environmentDefinitionDigestRE.MatchString(revision.Digest) {
		return nil, nil, ErrConflict
	}
	if previous.RevisionID != "" && (previous.CommitSHA != revision.CommitSHA || previous.Digest != revision.Digest) {
		return nil, nil, ErrConflict // an approved immutable identity cannot change in place
	}
	// The shared wait assessor currently proves SHA-1 Git identities only. A
	// store may retain SHA-256 revisions; do not break their approval or invent
	// an outcome when this assessor cannot prove them.
	if len(revision.CommitSHA) != 40 {
		delete(next, "rollout")
		return next, events, nil
	}
	tracker, err := rollout.NewTracker(revision.CommitSHA, source.EnvironmentSlug)
	if err != nil {
		return nil, nil, err
	}
	result, err := tracker.Observe(api.EnvironmentGitOpsStatusResponse{Source: source, WorkloadEvidence: evidence}, now)
	if err != nil {
		return nil, nil, err
	}
	if previous.RevisionID == "" {
		previous = environmentGitNotificationBaseline{Status: "progressing", Generation: source.Generation, RevisionID: revision.ID, CommitSHA: revision.CommitSHA, Digest: revision.Digest, IntentVersion: source.IntentVersion}
	}
	if result.RevisionID == revision.ID && result.DefinitionDigest == revision.Digest {
		previous.IntentVersion = source.IntentVersion
		previous.GraphID, previous.PlanHash = result.GraphID, evidence.PlanHash
		if result.Status == "serving" && !previous.ServingNotified || result.Status == "failed" && !previous.FailedNotified {
			event, err := environmentGitRolloutEvent(source, project, runID, previous, result.Status, result.Reason, result.Explanation, now)
			if err != nil {
				return nil, nil, err
			}
			events = append(events, event)
			previous.Status = result.Status
			previous.ServingNotified = previous.ServingNotified || result.Status == "serving"
			previous.FailedNotified = previous.FailedNotified || result.Status == "failed"
		}
	}
	next["rollout"] = previous
	return next, events, nil
}

func environmentGitRolloutEvent(source EnvironmentGitSource, project, runID string, baseline environmentGitNotificationBaseline,
	status, reason string, explanation *api.EnvironmentGitOpsExplanation, now time.Time) (environmentGitNotificationEvent, error) {
	id := uuid.NewString()
	payload := api.EnvironmentGitOpsChangedWebhookPayload{Version: 1, TransitionID: id, SourceID: source.ID, Project: project, Environment: source.EnvironmentSlug,
		Axis: "rollout", PreviousStatus: baseline.Status, Status: status, Generation: baseline.Generation, ObservedVersion: baseline.IntentVersion,
		ApprovedRevision: baseline.RevisionID, CommitSHA: baseline.CommitSHA, DefinitionDigest: baseline.Digest, GraphID: baseline.GraphID, PlanHash: baseline.PlanHash,
		RunID: runID, ObservedAt: now.UTC(), Changes: []api.EnvironmentGitOpsChange{}, BlockingReasons: []string{},
		DashboardPath: "/dashboard/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(source.EnvironmentSlug) + "/gitops"}
	if status != "serving" {
		payload.ErrorCode = reason
	}
	if status == "failed" && explanation != nil {
		for _, issue := range explanation.Issues {
			if issue.Resource != "" {
				name, ok := strings.CutPrefix(issue.Resource, "workload/")
				if !ok || !api.ValidAppSlug(name) {
					continue
				}
			}
			// Rebuild messages and actions from the allowlisted mapping. Never
			// forward graph errors, build logs or provider text to receivers.
			issue = diagnostics.Explain(issue.Resource, issue.Code)
			if slices.Contains(payload.Diagnostics, issue) {
				continue
			}
			if len(payload.Diagnostics) == api.EnvironmentGitOpsNotificationDetailsMax {
				payload.DetailsTruncated = true
				break
			}
			payload.Diagnostics = append(payload.Diagnostics, issue)
			if !slices.Contains(payload.BlockingReasons, issue.Code) {
				payload.BlockingReasons = append(payload.BlockingReasons, issue.Code)
			}
		}
	}
	body, err := json.Marshal(payload)
	return environmentGitNotificationEvent{id: id, body: body}, err
}

// A stale or incomplete report cannot advance the outcome baseline. Serving is
// still independently recomputed from the current graph, never from this plan.
func environmentGitRolloutReportCurrent(source EnvironmentGitSource, revision EnvironmentDesiredRevision, plan api.EnvironmentGitOpsPlan) bool {
	return plan.Manager == source.ID && plan.Revision == source.ApprovedRevisionID && plan.Generation == source.Generation &&
		plan.ObservedVersion == source.IntentVersion && plan.DesiredDigest == revision.Digest && plan.CommitSHA == revision.CommitSHA && environmentDefinitionDigestRE.MatchString(plan.Hash)
}
