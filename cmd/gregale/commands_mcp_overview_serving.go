package main

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func collectMCPOverviewServing(ctx context.Context, client *Client, options mcpOverviewOptions) mcpOverviewCollection {
	result := mcpOverviewCollection{Serving: mcpOverviewServing{State: "unknown"}}
	inspect := mcpOverviewInspection("Inspect serving replicas", "mcp", "status", "--app", options.App)
	if options.TokenEnv != "" {
		inspect.Arguments = append(inspect.Arguments, "--token-env", options.TokenEnv)
	}
	if options.Policy != "" {
		inspect.Arguments = append(inspect.Arguments, "--release-policy", options.Policy)
	}
	app, err := client.GetApp(ctx, options.App)
	if err != nil || app.Slug != options.App {
		result.Issues = append(result.Issues, mcpOverviewIssue{"serving", "serving_unavailable", "unknown", "Could not inspect the serving application. Check the selected connection, read permissions and connectivity.", inspect})
		return result
	}
	if isWorkerApp(app) {
		result.Serving.State = "degraded"
		result.Issues = append(result.Issues, mcpOverviewIssue{"serving", "serving_app_is_worker", "error", "--app must select the HTTP MCP application; use --worker-app for the worker pool.", nil})
		return result
	}
	deployments, err := client.ListAppDeploymentsAll(ctx, app.Slug)
	result.Holds = mcpOverviewHolds(app.Slug, deployments, time.Now())
	result.ReleasesComplete = err == nil
	if err != nil {
		result.Issues = append(result.Issues, mcpOverviewIssue{"releases", "deployments_unavailable", "unknown", "Serving deployment history could not be fully inspected; additional promotion holds may exist.", inspect})
		return result
	}
	report := mcpCollectCatalogStatusForDeployments(ctx, client, app, deployments, options.Endpoint, options.Roles, options.Timeout)
	if report.Healthy {
		result.Serving.State = "healthy"
	} else if len(report.Deployments) == 0 {
		result.Serving.State = "degraded"
		result.Issues = append(result.Issues, mcpOverviewIssue{"serving", "no_serving_deployment", "error", "The application has no live deployment receiving traffic.", inspect})
	} else if report.ExpectedReplicas == 0 || !report.Supported {
		result.Issues = append(result.Issues, mcpOverviewIssue{"serving", "serving_unverified", "unknown", "Serving catalog integrity or running replicas could not be verified.", inspect})
	} else {
		result.Serving.State = "degraded"
		result.Issues = append(result.Issues, mcpOverviewIssue{"serving", "serving_verification_failed", "error", "Serving replica health, catalog consistency or protocol qualification failed.", inspect})
	}
	attachMCPAdmissionHistory(ctx, client, &report)
	for i, deployment := range deployments {
		if i >= 5 {
			break
		}
		var history *mcpAdmissionHistory
		for j := range report.Deployments {
			if report.Deployments[j].DeploymentID == deployment.ID {
				history = report.Deployments[j].Admission
				break
			}
		}
		if history == nil {
			collected := collectMCPAdmissionHistory(ctx, client, deployment.ID, 50)
			history = &collected
		}
		result.Admissions = append(result.Admissions, *history)
	}
	convergence := collectMCPConvergence(ctx, client, app.Slug, app.ID)
	result.Convergence = &convergence
	report.Convergence = &convergence
	sanitizeMCPOverviewCatalog(&report)
	result.Serving.Catalog = &report
	return result
}

// Raw upstream error messages and preview URLs are unnecessary for an overview
// and may contain caller data. Detailed diagnostics remain in mcp status.
func sanitizeMCPOverviewCatalog(report *mcpCatalogStatusReport) {
	if report.Error != "" {
		report.Error = "Serving verification did not complete."
	}
	for i := range report.Deployments {
		deployment := &report.Deployments[i]
		if deployment.Error != "" {
			deployment.Error = "Deployment inspection failed."
		}
		if deployment.Health.Error != "" {
			deployment.Health.Error = "Replica catalog verification failed."
		}
		if !mcpCatalogSHA256Pattern.MatchString(deployment.Health.CatalogSHA256) {
			deployment.Health.CatalogSHA256 = ""
		}
		if deployment.Protocol.Error != "" {
			deployment.Protocol.Error = "Replica protocol qualification failed."
		}
		for j := range deployment.Health.Replicas {
			replica := &deployment.Health.Replicas[j]
			replica.HealthURL = ""
			if !mcpCatalogSHA256Pattern.MatchString(replica.CatalogSHA256) {
				replica.CatalogSHA256 = ""
			}
			switch replica.Status {
			case "ok", "healthy", "degraded":
			default:
				replica.Status = ""
			}
			if replica.Error != "" {
				replica.Error = "Replica health inspection failed."
			}
		}
		for j := range deployment.Protocol.Replicas {
			replica := &deployment.Protocol.Replicas[j]
			if replica.Error != "" {
				replica.Error = "Replica protocol qualification failed."
			}
			for k := range replica.Roles {
				if replica.Roles[k].Error != "" {
					replica.Roles[k].Error = "Caller catalog qualification failed."
				}
			}
		}
		for j := range deployment.Protocol.PublicRoles {
			if deployment.Protocol.PublicRoles[j].Error != "" {
				deployment.Protocol.PublicRoles[j].Error = "Public endpoint qualification failed."
			}
		}
	}
}

func mcpOverviewHolds(app string, deployments []api.DeploymentResponse, now time.Time) []mcpOverviewHold {
	holds := []mcpOverviewHold{}
	for _, deployment := range deployments {
		if deployment.PromotionHold == nil {
			continue
		}
		switch deployment.Status {
		case "failed", "cancelled", "retired", "deleted", "superseded":
			continue
		}
		hold := deployment.PromotionHold
		code := "promotion_held"
		if hold.Code == api.CodeMCPTaskCompatibilityGate {
			code = hold.Code
		}
		entry := mcpOverviewHold{App: app, DeploymentID: deployment.ID, Code: code, HeldAt: hold.HeldAt,
			RecoveryAttempts: hold.RecoveryAttempts, RecoveryInProgress: hold.RecoveryInProgress, NextRetryAt: hold.NextRetryAt,
			Inspect: *mcpOverviewInspection("Inspect deployment", "deployment", deployment.ID)}
		if !hold.RecoveryInProgress {
			entry.Retry = &mcpOverviewAction{Label: "Retry after resolving compatibility", Arguments: []string{"gregale", "deployment", "retry-hold", deployment.ID}, Mutating: true}
		}
		if !hold.HeldAt.IsZero() {
			age := now.Sub(hold.HeldAt).Seconds()
			if age < 0 {
				age = 0
			}
			entry.AgeSeconds = &age
		}
		holds = append(holds, entry)
	}
	sort.Slice(holds, func(i, j int) bool { return holds[i].DeploymentID < holds[j].DeploymentID })
	return holds
}
