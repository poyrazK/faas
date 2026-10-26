package main

import (
	"context"
	"errors"
	"fmt"
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
	var commitSHA, kind, status, failure, repo, appSlug, previewOf, scope, appStatus string
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
		       coalesce(preview_set.repo_full_name, nullif(parent.github_repo_full_name, ''),
		                nullif(a.github_repo_full_name, ''), nullif(p.repo_full_name, ''), ''),
		       a.slug, coalesce(a.preview_of_slug, ''), coalesce(a.preview_pr_number, 0), a.status,
		       coalesce(preview_set.installation_id, nullif(parent.github_install_id, 0),
		                nullif(a.github_install_id, 0), nullif(p.install_id, 0), 0)
		from deployments d
		join apps a on a.id = d.app_id
		left join projects p on p.id = a.project_id
		left join apps parent
		  on parent.account_id = a.account_id
		 and parent.slug = a.preview_of_slug
		 and parent.deleted_at is null
		left join lateral (
			select installation_id, repo_full_name from pr_preview_sets
			where pr_number = a.preview_pr_number and closed_at is null
			  and member_app_ids @> array[a.id::text]
			order by updated_at desc limit 1
		) preview_set on true
		where d.id = $1`, deploymentID).Scan(
		&commitSHA, &kind, &status, &failure, &scope, &rolloutState, &rolloutAbortedReason,
		&canaryStep, &canaryTotalSteps, &trafficPercent, &reason, &tag, &deployedBy, &prNumber,
		&repo, &appSlug, &previewOf, &previewPRNumber, &appStatus, &installationID)
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
	if appStatus == string(state.AppDeleted) && (kind == string(state.DeploymentKindPreview) || previewOf != "") {
		return syncTornDownPreview(ctx, checks, tornDownPreview{
			deploymentID: deploymentID, installationID: installationID, repo: repo, commitSHA: commitSHA,
			environment: githubDeploymentEnvironment(kind, scope, appSlug, previewOf), appSlug: appSlug, prNumber: previewPRNumber,
		})
	}
	phase, ok := checkPhaseForDeploymentStatusForRollout(status, rolloutState, canaryTotalSteps)
	if !ok {
		return nil
	}
	domain := githubd.AppsDomain()
	// Annotation fields are customer-controlled text. Keep the Check Run
	// summary and PR comment single-line and predictable; the provider writer
	// applies the same compaction to the Deployment payload.
	deployedByText := strings.Join(strings.Fields(deployedBy), " ")
	reasonText := strings.Join(strings.Fields(reason), " ")
	deploymentURL := githubd.DashboardDeploymentURL(appSlug, deploymentID)
	logsURL := githubd.DashboardLogsURL(appSlug)
	environmentURL := fmt.Sprintf("https://%s.%s", appSlug, domain)
	environment := githubDeploymentEnvironment(kind, scope, appSlug, previewOf)
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
		var setCheck previewSetCheck
		if previewPRNumber > 0 && prNumber > 0 {
			setCheck, err = loadPRPreviewSetCheck(ctx, pool, installationID, repo, prNumber, commitSHA)
			if err != nil {
				return err
			}
			if !setCheck.CurrentHead {
				// A stale deployment (or a preview from before revision sets were
				// recorded) must not overwrite the current PR-level check.
				return nil
			}
			phase, summary = setCheck.Phase, setCheck.Summary
			if setCheck.RootSlug != "" {
				environmentURL = fmt.Sprintf("https://%s.%s", setCheck.RootSlug, domain)
			}
		}
		if err := checks.WritePreviewCheckForInstallation(ctx, installationID, repo, commitSHA, phase,
			environmentURL, summary); err != nil {
			return err
		}
		if previewPRNumber > 0 {
			// Check Runs are the durable status source. The comment is a
			// best-effort companion: a missing Issues:write grant must not
			// prevent the Check Run worker from making progress.
			previewURL := "https://" + appSlug + "." + domain
			commentSlug, commentStatus := appSlug, status
			if setCheck.CurrentHead && setCheck.RootSlug != "" {
				// Every sibling updates the same root comment with the aggregate
				// result. A live root must not advertise a complete environment
				// while a dependency is still building or has failed.
				previewURL = environmentURL
				commentSlug, commentStatus = setCheck.RootSlug, setCheck.CommentSummary
			}
			marker := "<!-- gregale-preview:" + commentSlug + " -->"
			// The details page is the root preview's dashboard page: it shows
			// the whole workload set and its app menu tears the environment down.
			body := fmt.Sprintf("%s\n### Gregale preview\n\nPreview status: **%s**\n\n[Open preview](%s) · [Preview details and teardown](%s) · [Latest workload deployment](%s) · [Logs](%s)\n\nCommit: `%s`",
				marker, commentStatus, previewURL, githubd.DashboardAppURL(commentSlug), deploymentURL, logsURL, commitSHA)
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
	if commitSHA == "" {
		return false, fmt.Errorf("githubd: deployment check %s missing commit", deploymentID)
	}
	// Disconnecting a repository clears its repo and installation metadata.
	// Historical deployment transitions can still leave a coalesced outbox row,
	// but there is no external Check Run target to update. Completing that row as
	// a no-op prevents a permanent retry/dead-letter loop after an intentional
	// disconnect. A missing commit remains an invalid deployment record above.
	if repo == "" || installationID <= 0 {
		return false, nil
	}
	return true, nil
}

// githubDeploymentEnvironment names the GitHub environment a deployment
// reports to. A preview reports to its production app's preview
// environment, preview/<app>: GitHub keeps every environment a deployment
// names, so naming one per pull request would leave a new environment in
// the repository for every PR.
func githubDeploymentEnvironment(kind, scope, appSlug, previewOf string) string {
	if kind == string(state.DeploymentKindPreview) {
		if previewOf != "" {
			appSlug = previewOf
		}
		return "preview/" + appSlug
	}
	prefix := "production"
	if scope != "" && scope != string(state.DefaultEnvScope) {
		prefix = scope
	}
	return prefix + "/" + appSlug
}

type tornDownPreview struct {
	deploymentID, repo, commitSHA, environment, appSlug string
	installationID                                      int64
	prNumber                                            int
}

// syncTornDownPreview reports a torn-down preview to GitHub. Its deployment
// goes inactive, so the PR stops offering a dead preview link, and the PR's
// status comment, when this preview owns one, says the preview is gone.
// The Check Run keeps the result of the commit it reported.
func syncTornDownPreview(ctx context.Context, checks *githubd.ChecksAPI, preview tornDownPreview) error {
	if checks.Deployments != nil {
		_, err := checks.Deployments.GitHubDeploymentID(ctx, preview.deploymentID)
		switch {
		case err == nil:
			if err := checks.WriteGitHubDeploymentStatus(ctx, githubd.GitHubDeploymentUpdate{
				LocalDeploymentID: preview.deploymentID, InstallationID: preview.installationID,
				RepoFullName: preview.repo, CommitSHA: preview.commitSHA, Ref: preview.commitSHA,
				Environment: preview.environment, Status: githubd.GitHubDeploymentStatusTornDown,
				Description: "Gregale preview torn down", TransientEnvironment: true,
			}); err != nil {
				return err
			}
		case !errors.Is(err, githubd.ErrGitHubDeploymentNotFound):
			return err
		}
	}
	if preview.prNumber <= 0 {
		return nil
	}
	marker := "<!-- gregale-preview:" + preview.appSlug + " -->"
	body := fmt.Sprintf("%s\n### Gregale preview — torn down\n\nThe preview was torn down: its pull request closed, its lease expired, or it was destroyed. Push a commit to an open pull request to create it again.\n\nCommit: `%s`",
		marker, preview.commitSHA)
	// Best effort, as for every status comment: Check Runs and deployments
	// are the durable status.
	_ = checks.UpdatePreviewComment(ctx, preview.installationID, preview.repo, preview.prNumber, marker, body)
	return nil
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
