package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// syncDeploymentCheck projects current durable deployment state onto its
// stable GitHub Check Run. Callers retry returned errors through the durable
// check outbox; no status transition depends on a lossy LISTEN notification.
func syncDeploymentCheck(ctx context.Context, pool *pgxpool.Pool, checks *githubd.ChecksAPI, deploymentID string) error {
	if deploymentID == "" {
		return fmt.Errorf("githubd: deployment check: empty deployment id")
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
		where d.id = $1`, deploymentID).Scan(
		&commitSHA, &kind, &status, &failure, &repo, &appSlug, &previewOf, &installationID)
	if err != nil {
		return fmt.Errorf("githubd: deployment check lookup %s: %w", deploymentID, err)
	}
	if commitSHA == "" || repo == "" || installationID <= 0 {
		return fmt.Errorf("githubd: deployment check %s missing commit, repo, or installation", deploymentID)
	}
	if kind != string(state.DeploymentKindGitHub) && kind != string(state.DeploymentKindPreview) {
		return nil
	}
	phase, ok := checkPhaseForDeploymentStatus(status)
	if !ok {
		return nil
	}
	summary := fmt.Sprintf("Gregale deployment %s is %s.", deploymentID, status)
	if failure != "" {
		summary += " " + failure
	}
	if kind == string(state.DeploymentKindPreview) || previewOf != "" {
		return checks.WritePreviewCheckForInstallation(ctx, installationID, repo, commitSHA, phase,
			"https://"+appSlug+".gregale.dev", summary)
	}
	return checks.WriteAppCheck(ctx, installationID, repo, commitSHA, appSlug, phase, "", summary)
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
