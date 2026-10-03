package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// previewEnvironmentForApp keeps developer, legacy, and manually created PR
// previews on their existing app-level CLI contract. Once a recorded set is
// present, callers must use its whole-set readiness instead.
func previewEnvironmentForApp(ctx context.Context, client *Client, app api.AppResponse) (*api.PreviewEnvironmentStatusResponse, error) {
	if app.PreviewPRNumber <= 0 {
		return nil, nil
	}
	environment, err := client.GetPreviewEnvironmentStatus(ctx, app.Slug)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == 404 {
			return nil, nil
		}
		return nil, err
	}
	if environment.RootSlug != app.Slug || environment.PRNumber != app.PreviewPRNumber || environment.CommitSHA == "" {
		return nil, fmt.Errorf("preview environment status does not match %s", app.Slug)
	}
	return &environment, nil
}

func renderPreviewEnvironmentDetails(environment api.PreviewEnvironmentStatusResponse) {
	PrintProgress(osStdout, "Environment:  %s (%d/%d workloads live)", environment.Phase,
		environment.LiveWorkloads, environment.TotalWorkloads)
	PrintProgress(osStdout, "Commit:       %s", environment.CommitSHA)
	for _, member := range environment.Members {
		PrintProgress(osStdout, "  %-20s %s", previewMemberName(member), previewMemberStatus(member))
		if member.ExpiresAt != nil {
			PrintProgress(osStdout, "    Expires: %s", previewExpiry(member.ExpiresAt))
		}
		if member.Changes != nil {
			artifact := "matches production"
			switch {
			case member.DeploymentID == "":
				artifact = "not deployed at current PR head"
			case member.Changes.PreviewArtifact.DeploymentID == "":
				artifact = "current-head artifact unavailable"
			case member.Changes.ProductionArtifact.DeploymentID == "":
				artifact = "production baseline unavailable"
			case member.Changes.ArtifactChanged:
				artifact = "differs from production"
			}
			PrintProgress(osStdout, "    Artifact: %s", artifact)
			if len(member.Changes.ConfigurationChangedGroups) == 0 {
				PrintProgress(osStdout, "    Config:   no changed groups")
			} else {
				PrintProgress(osStdout, "    Config:   %s", strings.Join(member.Changes.ConfigurationChangedGroups, ", "))
			}
		}
		if member.Links != nil {
			PrintProgress(osStdout, "    URL:      %s", member.Links.URL)
			PrintProgress(osStdout, "    Logs:     %s", member.Links.Logs)
			PrintProgress(osStdout, "    Metrics:  %s", member.Links.Metrics)
			PrintProgress(osStdout, "    Config:   %s", member.Links.Configuration)
		}
	}
	if environment.Phase == "failed" {
		PrintProgress(osStdout, "Next:         %s", previewEnvironmentNextAction(environment))
	}
}

func previewMemberName(member api.PreviewEnvironmentMemberResponse) string {
	if name := strings.TrimSpace(member.WorkloadName); name != "" {
		return name
	}
	if member.Slug != "" {
		return member.Slug
	}
	return member.AppID
}

func previewMemberStatus(member api.PreviewEnvironmentMemberResponse) string {
	if member.AppStatus != "active" && member.AppStatus != "evicted_cold" {
		return "unavailable (" + member.AppStatus + ")"
	}
	if member.PreviewState != "open" {
		return "unavailable (preview " + member.PreviewState + ")"
	}
	return member.DeploymentStatus
}

func previewEnvironmentNextAction(environment api.PreviewEnvironmentStatusResponse) string {
	if environment.Phase == "closed" {
		return "reopen the pull request to create a fresh preview"
	}
	for _, member := range environment.Members {
		status := previewMemberStatus(member)
		if (status == "failed" || status == "cancelled" || status == "superseded" || strings.HasPrefix(status, "unavailable")) &&
			member.Slug != "" && member.DeploymentID != "" {
			return fmt.Sprintf("gregale logs %s --deployment %s --follow", member.Slug, member.DeploymentID)
		}
	}
	for _, member := range environment.Members {
		if member.DeploymentStatus != "live" && member.Slug != "" && member.DeploymentID != "" {
			return fmt.Sprintf("gregale logs %s --deployment %s --follow", member.Slug, member.DeploymentID)
		}
	}
	return fmt.Sprintf("gregale preview show %s", environment.RootSlug)
}

// previewEnvironmentRootDeployment avoids displaying an old-head app deployment
// as the receipt for the current PR environment.
func previewEnvironmentRootDeployment(state api.PreviewStatusResponse, environment api.PreviewEnvironmentStatusResponse) *api.DeploymentResponse {
	for _, member := range environment.Members {
		if member.AppID != state.App.ID || member.DeploymentID == "" {
			continue
		}
		if state.LatestDeployment != nil && state.LatestDeployment.ID == member.DeploymentID {
			return state.LatestDeployment
		}
		return &api.DeploymentResponse{ID: member.DeploymentID, AppID: member.AppID,
			Status: member.DeploymentStatus, CommitSHA: environment.CommitSHA}
	}
	return nil
}

func renderPreviewEnvironmentProgress(environment api.PreviewEnvironmentStatusResponse, previousSHA string, previous map[string]string) (string, map[string]string) {
	if previousSHA != environment.CommitSHA {
		PrintProgress(osStdout, "Preview commit: %s", environment.CommitSHA)
		previous = nil
	}
	next := make(map[string]string, len(environment.Members))
	for _, member := range environment.Members {
		status := previewMemberStatus(member)
		next[member.AppID] = status
		if previous == nil || previous[member.AppID] != status {
			PrintProgress(osStdout, "%s: %s", previewMemberName(member), status)
		}
	}
	return environment.CommitSHA, next
}
