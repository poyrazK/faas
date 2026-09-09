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
	var rolloutState, rolloutAbortedReason string
	var canaryStep, canaryTotalSteps, trafficPercent int
	var reason, tag, deployedBy string
	var previewPRNumber, prNumber int
	var installationID int64
	err := pool.QueryRow(ctx, `
		select coalesce(d.commit_sha, ''), d.kind, d.status, coalesce(d.error, ''), coalesce(d.scope, 'default'),
		       coalesce(d.rollout_state, ''), coalesce(d.rollout_aborted_reason, ''),
		       coalesce(d.canary_step, 0), coalesce(d.canary_total_steps, 0), coalesce(d.traffic_percent, 0),
		       coalesce(d.reason, ''), coalesce(d.tag, ''), coalesce(nullif(d.deployed_by, ''), d.pusher_login, ''), coalesce(d.pr_number, 0),
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
		&commitSHA, &kind, &status, &failure, &scope, &rolloutState, &rolloutAbortedReason,
		&canaryStep, &canaryTotalSteps, &trafficPercent, &reason, &tag, &deployedBy, &prNumber,
		&repo, &appSlug, &previewOf, &previewPRNumber, &installationID)
	if err != nil {
		return fmt.Errorf("githubd: deployment check lookup %s: %w", deploymentID, err)
	}
	// Pre-annotation preview rows kept the PR number on the preview app;
	// use it when the deployment row predates the annotation columns.
	if prNumber <= 0 && previewPRNumber > 0 {
		prNumber = previewPRNumber
	}
	project, err := validateDeploymentCheckTarget(deploymentID, kind, commitSHA, repo, installationID)
	if err != nil {
		return err
	}
	if !project {
		return nil
	}
	phase, ok := checkPhaseForDeploymentStatusForRollout(status, rolloutState, canaryTotalSteps)
	if !ok {
		return nil
	}
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" || domain == "apps.gregale.dev" {
		domain = "gregale.dev"
	}
	// Annotation fields are customer-controlled text. Keep the Check Run
	// summary and PR comment single-line and predictable; the provider writer
	// applies the same compaction to the Deployment payload.
	deployedByText := strings.Join(strings.Fields(deployedBy), " ")
	reasonText := strings.Join(strings.Fields(reason), " ")
	deploymentURL := fmt.Sprintf("https://%s/dashboard/apps/%s/deployments/%s", domain, appSlug, deploymentID)
	logsURL := fmt.Sprintf("https://%s/v1/deployments/%s/logs", domain, deploymentID)
	environmentURL := fmt.Sprintf("https://%s.%s", appSlug, domain)
	environment := githubDeploymentEnvironment(kind, scope, appSlug)
	if err := checks.WriteGitHubDeploymentStatus(ctx, githubd.GitHubDeploymentUpdate{
		LocalDeploymentID:     deploymentID,
		InstallationID:        installationID,
		RepoFullName:          repo,
		CommitSHA:             commitSHA,
		Ref:                   commitSHA,
		Environment:           environment,
		Status:                status,
		RolloutState:          rolloutState,
		CanaryStep:            canaryStep,
		CanaryTotalSteps:      canaryTotalSteps,
		TrafficPercent:        trafficPercent,
		RolloutAbortedReason:  rolloutAbortedReason,
		Description:           fmt.Sprintf("Gregale deployment %s for %s", deploymentID, appSlug),
		DeployedBy:            deployedBy,
		PRNumber:              prNumber,
		Reason:                reason,
		Tag:                   tag,
		TargetURL:             deploymentURL,
		EnvironmentURL:        environmentURL,
		LogURL:                logsURL,
		TransientEnvironment:  kind == string(state.DeploymentKindPreview) || previewOf != "",
		ProductionEnvironment: kind == string(state.DeploymentKindGitHub) && scope == string(state.DefaultEnvScope),
	}); err != nil {
		return err
	}
	summary := fmt.Sprintf("Gregale deployment %s is %s.", deploymentID, status)
	if canaryTotalSteps > 0 && (rolloutState == "pending" || rolloutState == "rolling_out") {
		summary += fmt.Sprintf(" Canary rollout is in progress at stage %d/%d (%d%% traffic); safety gates must pass before promotion.", canaryStep, canaryTotalSteps, trafficPercent)
	} else if canaryTotalSteps > 0 && rolloutState == "aborted" {
		summary += " Canary rollout was aborted."
		if rolloutAbortedReason != "" {
			summary += " Reason: " + strings.Join(strings.Fields(rolloutAbortedReason), " ") + "."
		}
	} else if rolloutState == "complete" && canaryTotalSteps > 0 {
		summary += fmt.Sprintf(" Canary rollout completed at %d/%d (100%% traffic).", canaryStep, canaryTotalSteps)
	}
	if deployedByText != "" {
		summary += " Deployed by " + deployedByText + "."
	}
	if prNumber > 0 {
		summary += fmt.Sprintf(" PR #%d.", prNumber)
	}
	if reasonText != "" {
		summary += " Reason: " + reasonText + "."
	}
	if failure != "" {
		summary += " " + failure
	}
	if kind == string(state.DeploymentKindPreview) || previewOf != "" {
		if err := checks.WritePreviewCheckForInstallation(ctx, installationID, repo, commitSHA, phase,
			environmentURL, summary); err != nil {
			return err
		}
		if previewPRNumber > 0 {
			// Check Runs are the durable status source. The comment is a
			// best-effort companion: a missing Issues:write grant must not
			// prevent the Check Run worker from making progress.
			previewURL := "https://" + appSlug + "." + domain
			dashboardBase := "https://" + domain
			marker := "<!-- gregale-preview:" + appSlug + " -->"
			body := fmt.Sprintf("%s\n### Gregale preview — %s\n\nPreview status: **%s**.\n\n[Open preview](%s) · [Deployment details](%s/dashboard/apps/%s/deployments/%s) · [Deployment logs](%s/v1/deployments/%s/logs) · [Destroy preview](%s/dashboard/apps/%s/preview/%s/destroy)\n\nCommit: `%s`", marker, status, status, previewURL, dashboardBase, appSlug, deploymentID, dashboardBase, deploymentID, dashboardBase, previewOf, appSlug, commitSHA)
			if deployedByText != "" {
				body += "\nDeployed by: **" + deployedByText + "**"
			}
			if prNumber > 0 {
				body += fmt.Sprintf("\nPull request: **#%d**", prNumber)
			}
			if reasonText != "" {
				body += "\nReason: " + reasonText
			}
			_ = checks.UpsertPreviewComment(ctx, installationID, repo, previewPRNumber, marker, body)
		}
		return nil
	}
	return checks.WriteScopedAppCheck(ctx, installationID, repo, commitSHA, appSlug, scope, phase, "", summary)
}

func validateDeploymentCheckTarget(deploymentID, kind, commitSHA, repo string, installationID int64) (bool, error) {
	if kind != string(state.DeploymentKindGitHub) && kind != string(state.DeploymentKindPreview) {
		return false, nil
	}
	if commitSHA == "" || repo == "" || installationID <= 0 {
		return false, fmt.Errorf("githubd: deployment check %s missing commit, repo, or installation", deploymentID)
	}
	return true, nil
}

func githubDeploymentEnvironment(kind, scope, appSlug string) string {
	prefix := "production"
	if kind == string(state.DeploymentKindPreview) {
		prefix = "preview"
	} else if scope != "" && scope != string(state.DefaultEnvScope) {
		prefix = scope
	}
	return prefix + "/" + appSlug
}

func checkPhaseForDeploymentStatusForRollout(status, rolloutState string, canaryTotalSteps int) (githubdgrpc.CheckPhase, bool) {
	switch state.DeploymentStatus(status) {
	case state.DeployPending:
		return githubdgrpc.CheckPhaseQueued, true
	case state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting:
		return githubdgrpc.CheckPhaseBuilding, true
	case state.DeployLive:
		if canaryTotalSteps <= 0 {
			return githubdgrpc.CheckPhaseLive, true
		}
		switch rolloutState {
		case "pending", "rolling_out":
			return githubdgrpc.CheckPhaseBuilding, true
		case "aborted":
			return githubdgrpc.CheckPhaseFailed, true
		}
		return githubdgrpc.CheckPhaseLive, true
	case state.DeployFailed, state.DeployCancelled, state.DeploySuperseded:
		return githubdgrpc.CheckPhaseFailed, true
	default:
		return githubdgrpc.CheckPhaseUnspecified, false
	}
}
