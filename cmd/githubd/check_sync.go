package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// syncDeploymentCheck projects the durable deployment state machine onto the
// Check Run created by the webhook dispatcher. The repo is resolved from the
// app binding, its project, or (for previews) the bound parent app.
func syncDeploymentCheck(ctx context.Context, pool *pgxpool.Pool, checks *githubd.ChecksAPI, payload string, log *slog.Logger) {
	var event struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil || event.DeploymentID == "" {
		return
	}
	var commitSHA, kind, status, failure, repo, appSlug, previewOf string
	var installationID int64
	err := pool.QueryRow(ctx, `
		select coalesce(d.commit_sha, ''), d.kind, d.status, coalesce(d.error, ''),
		       coalesce(parent.github_repo_full_name, a.github_repo_full_name, p.repo_full_name, ''),
		       a.slug, coalesce(a.preview_of_slug, ''),
		       coalesce(parent.github_install_id, a.github_install_id, 0)
		from deployments d
		join apps a on a.id = d.app_id
		left join projects p on p.id = a.project_id
		left join apps parent
		  on parent.account_id = a.account_id
		 and parent.slug = a.preview_of_slug
		 and parent.deleted_at is null
		where d.id = $1`, event.DeploymentID).Scan(
		&commitSHA, &kind, &status, &failure, &repo, &appSlug, &previewOf, &installationID)
	if err != nil || commitSHA == "" || repo == "" || installationID <= 0 {
		if err != nil {
			log.Debug("githubd: deployment check lookup skipped", "deployment_id", event.DeploymentID, "err", err)
		}
		return
	}
	if kind != string(state.DeploymentKindGitHub) && kind != string(state.DeploymentKindPreview) {
		return
	}
	phase, ok := checkPhaseForDeploymentStatus(status)
	if !ok {
		return
	}
	summary := fmt.Sprintf("Gregale deployment %s is %s.", event.DeploymentID, status)
	if failure != "" {
		summary += " " + failure
	}
	var writeErr error
	if kind == string(state.DeploymentKindPreview) || previewOf != "" {
		writeErr = checks.WritePreviewCheckForInstallation(ctx, installationID, repo, commitSHA, phase,
			"https://"+appSlug+".gregale.dev", summary)
	} else {
		writeErr = checks.WriteAppCheck(ctx, installationID, repo, commitSHA, appSlug, phase, "", summary)
	}
	if writeErr != nil {
		log.Warn("githubd: sync deployment check", "deployment_id", event.DeploymentID,
			"repo", repo, "status", status, "err", writeErr)
	}
}

func checkPhaseForDeploymentStatus(status string) (githubdgrpc.CheckPhase, bool) {
	switch state.DeploymentStatus(status) {
	case state.DeployPending:
		return githubdgrpc.CheckPhaseQueued, true
	case state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting:
		return githubdgrpc.CheckPhaseBuilding, true
	case state.DeployLive:
		return githubdgrpc.CheckPhaseLive, true
	case state.DeployFailed, state.DeployCancelled, state.DeploySuperseded:
		return githubdgrpc.CheckPhaseFailed, true
	default:
		return githubdgrpc.CheckPhaseUnspecified, false
	}
}
