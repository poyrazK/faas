package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	var commitSHA, kind, status, failure, repo, appSlug, previewOf, scope string
	var previewPRNumber int
	var installationID int64
	err := pool.QueryRow(ctx, `
		select coalesce(d.commit_sha, ''), d.kind, d.status, coalesce(d.error, ''), coalesce(d.scope, 'default'),
		       coalesce(parent.github_repo_full_name, a.github_repo_full_name, p.repo_full_name, ''),
		       a.slug, coalesce(a.preview_of_slug, ''), coalesce(a.preview_pr_number, 0),
		       coalesce(parent.github_install_id, a.github_install_id, 0)
		from deployments d
		join apps a on a.id = d.app_id
		left join projects p on p.id = a.project_id
		left join apps parent
		  on parent.account_id = a.account_id
		 and parent.slug = a.preview_of_slug
		 and parent.deleted_at is null
		where d.id = $1`, deploymentID).Scan(
		&commitSHA, &kind, &status, &failure, &scope, &repo, &appSlug, &previewOf, &previewPRNumber, &installationID)
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
		if err := checks.WritePreviewCheckForInstallation(ctx, installationID, repo, commitSHA, phase,
			"https://"+appSlug+".gregale.dev", summary); err != nil {
			return err
		}
		if previewPRNumber > 0 {
			// Check Runs are the durable status source. The comment is a
			// best-effort companion: a missing Issues:write grant must not
			// prevent the Check Run worker from making progress.
			domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
			if domain == "" {
				domain = "gregale.dev"
			}
			previewURL := "https://" + appSlug + "." + domain
			dashboardBase := "https://" + domain
			marker := "<!-- gregale-preview:" + appSlug + " -->"
			body := fmt.Sprintf("%s\n### Gregale preview — %s\n\nPreview status: **%s**.\n\n[Open preview](%s) · [Deployment details](%s/dashboard/apps/%s/deployments/%s) · [Deployment logs](%s/v1/deployments/%s/logs) · [Destroy preview](%s/dashboard/apps/%s/preview/%s/destroy)\n\nCommit: `%s`", marker, status, status, previewURL, dashboardBase, appSlug, deploymentID, dashboardBase, deploymentID, dashboardBase, previewOf, appSlug, commitSHA)
			if commentErr := checks.UpsertPreviewComment(ctx, installationID, repo, previewPRNumber, marker, body); commentErr != nil {
				// Deliberately ignored; see comment above.
			}
		}
		return nil
	}
	return checks.WriteScopedAppCheck(ctx, installationID, repo, commitSHA, appSlug, scope, phase, "", summary)
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
