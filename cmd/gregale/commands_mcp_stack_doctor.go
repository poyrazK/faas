package main

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpStackDoctorCheck struct {
	Name   string             `json:"name"`
	Status string             `json:"status"`
	Detail string             `json:"detail,omitempty"`
	Action *mcpOverviewAction `json:"action,omitempty"`
}
type mcpStackDoctorReport struct {
	Version  int                   `json:"version"`
	OK       bool                  `json:"ok"`
	Checks   []mcpStackDoctorCheck `json:"checks"`
	Overview mcpOverviewReport     `json:"overview"`
}

func collectMCPStackDoctor(ctx context.Context, client *Client, options mcpOverviewOptions, config *mcphosting.Config) mcpStackDoctorReport {
	// Provider and stack evidence share the same deadline, but fail independently.
	auth := make(chan []mcphosting.Check, 1)
	go func() {
		checks := collectMCPDoctorBindings(ctx, client, options)
		auth <- append(checks, collectMCPDoctorAuth(ctx, client, options, config)...)
	}()
	return buildMCPStackDoctor(collectMCPOverview(ctx, client, options), <-auth)
}

func collectMCPDoctorAuth(ctx context.Context, client *Client, options mcpOverviewOptions, config *mcphosting.Config) []mcphosting.Check {
	if config == nil {
		return []mcphosting.Check{{Name: "oauth_configuration", Status: "unknown", Detail: "Supply --path with the reviewed source configuration to verify OAuth expectations."}}
	}
	if config.Auth.Mode == "open" {
		return []mcphosting.Check{{Name: "oauth_configuration", Status: "passed", Detail: "Reviewed source declares open access; OAuth provider checks are not applicable. This does not verify deployment configuration."}}
	}
	checks := mcphosting.CheckOAuthProvider(ctx, config.Auth)
	check := mcphosting.Check{Name: "oauth_resource_discovery", Status: "unknown", Detail: "Could not inspect the serving endpoint."}
	app, err := client.GetApp(ctx, options.App)
	if err == nil && app.Slug == options.App {
		endpoint, urlErr := (mcphosting.Config{Endpoint: options.Endpoint}).URL(canonicalAppURL(app))
		if urlErr == nil {
			probe, probeErr := mcphosting.NewClient(endpoint, "", mcphosting.ProtocolVersion)
			if probeErr == nil {
				probe.ExpectedAuth = &config.Auth
				if probe.VerifyOAuth(ctx) == nil {
					check.Status = "passed"
					check.Detail = "Unauthenticated and malformed-token rejection, audience, issuer and advertised scopes match the reviewed configuration."
				} else {
					check.Status = "failed"
					check.Detail = "OAuth challenge or protected-resource metadata does not match the reviewed configuration; inspect issuer, resource and scopes."
				}
			}
		}
	}
	return append(checks, check)
}

func buildMCPStackDoctor(overview mcpOverviewReport, auth []mcphosting.Check) mcpStackDoctorReport {
	report := mcpStackDoctorReport{Version: 1, OK: true, Overview: overview, Checks: []mcpStackDoctorCheck{}}
	add := func(name, status, detail string, action *mcpOverviewAction) {
		report.Checks = append(report.Checks, mcpStackDoctorCheck{name, status, detail, action})
		if status != "passed" {
			report.OK = false
		}
	}
	for _, component := range []struct{ name, state string }{{"serving", overview.Serving.State}, {"tasks", overview.Tasks.State}, {"releases", overview.Releases.State}} {
		if component.state == "not_requested" {
			continue
		}
		state := "unknown"
		switch component.state {
		case "healthy":
			state = "passed"
		case "degraded", "blocked":
			state = "failed"
		}
		add(component.name, state, "Current hosting evidence: "+component.state, nil)
	}
	for _, issue := range overview.Diagnostics {
		if issue.Severity == "info" {
			continue
		}
		state := "unknown"
		if issue.Severity == "error" {
			state = "failed"
		}
		add(issue.Code, state, issue.Message, issue.Action)
	}
	for _, hold := range overview.Releases.Holds {
		add("promotion_hold", "failed", "Inspect the deployment hold before retrying promotion.", &hold.Inspect)
	}
	for _, check := range auth {
		add(check.Name, check.Status, check.Detail, nil)
	}
	return report
}

func writeMCPStackDoctor(report mcpStackDoctorReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP stack doctor for %s (ok=%t)\n", report.Overview.App, report.OK)
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Name, check.Status, check.Detail)
			if check.Action != nil {
				_, _ = fmt.Fprintf(osStdout, "  Suggested command arguments: %q\n", check.Action.Arguments)
			}
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}

func collectMCPDoctorBindings(ctx context.Context, client *Client, options mcpOverviewOptions) []mcphosting.Check {
	if options.Worker == "" {
		return nil
	}
	web, webErr := client.GetApp(ctx, options.App)
	worker, workerErr := client.GetApp(ctx, options.Worker)
	if webErr != nil || workerErr != nil || web.Slug != options.App || worker.Slug != options.Worker {
		return []mcphosting.Check{{Name: "task_process_bindings", Status: "unknown", Detail: "Could not inspect both web and worker settings."}}
	}
	checks := []mcphosting.Check{}
	for _, entry := range []struct {
		app  api.AppResponse
		role string
	}{{web, "web"}, {worker, "worker"}} {
		role, exists := entry.app.Manifest.Env["MCP_TASKS_ROLE"]
		_, sealed := entry.app.Manifest.EnvSecrets["MCP_TASKS_ROLE"]
		status := "passed"
		if !exists || sealed {
			status = "unknown"
		} else if role != entry.role {
			status = "failed"
		}
		checks = append(checks, mcphosting.Check{Name: "task_" + entry.role + "_role", Status: status, Detail: "Explicit non-secret process role must match the selected application; review runtime.env and deploy the corrected role."})
	}
	status := "passed"
	if !mcpTasksObserverNamespacesMatch(web, worker) {
		status = "unknown"
		if web.Manifest.Env[mcpTasksObserverNamespaceEnv] != "" && worker.Manifest.Env[mcpTasksObserverNamespaceEnv] != "" {
			status = "failed"
		}
	}
	checks = append(checks, mcphosting.Check{Name: "task_web_worker_namespace", Status: status, Detail: "Web and worker require the same explicit non-secret queue namespace; review runtime.env before a coordinated rollout."})
	status = "passed"
	if !mcpTasksObserverMetricsTargetMatches(worker, options.Worker) {
		status = "failed"
	}
	return append(checks, mcphosting.Check{Name: "task_worker_metrics_target", Status: status, Detail: "Worker metric destination must identify the selected worker app; review MCP_TASKS_SCALING_APP_SLUG in runtime.env."})
}
