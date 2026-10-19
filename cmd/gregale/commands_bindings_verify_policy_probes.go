package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func executeBindingPolicyProbe(ctx context.Context, client *bindingPolicyProbeClient, step bindingPolicyProbePlan, interval, timeout time.Duration) (bindingPolicyProbeResult, string, int, error) {
	result := bindingPolicyProbeResult{Type: step.Type, Name: step.Name, Binding: step.Binding, Scope: client.snapshot.policy.Scope, Prefix: step.Prefix, Status: "failed"}
	var task api.AppTaskResponse
	var title string
	var code int
	var err error
	slug := client.snapshot.slug
	switch step.Type {
	case api.BindingTypeService:
		var report api.ServiceBindingProbeReport
		report, task, title, code, err = executeServiceBindingProbe(ctx, client, slug, step.Name, interval, timeout)
		if code == 0 && report.TargetDeploymentID != step.TargetDeploymentID {
			code, err = 1, fmt.Errorf("service canary did not confirm the proposed target deployment")
		}
		result.ServiceReport, result.TaskID = &report, report.TaskID
	case api.BindingTypePostgres:
		var report api.PostgresBindingProbeReport
		report, task, title, code, err = executePostgresBindingProbe(ctx, client, slug, step.Binding, interval, timeout)
		result.PostgresReport, result.TaskID = &report, report.TaskID
	case api.BindingTypeObjectStorage:
		mode := step.ProbeMode
		if mode == "read" {
			mode = "" // Existing listing canaries use the legacy empty mode.
		}
		var report api.ObjectStorageBindingProbeReport
		report, task, title, code, err = executeObjectStorageBindingProbeMode(ctx, client, slug, step.Prefix, mode, interval, timeout)
		result.Report, result.TaskID = &report, report.TaskID
	case api.BindingTypeOutbound:
		var report api.OutboundBindingProbeReport
		report, task, title, code, err = executeOutboundBindingProbe(ctx, client, slug, client.snapshot.outboundIDs[step.Name], interval, timeout)
		result.OutboundReport, result.TaskID = &report, report.TaskID
	default:
		return result, "Unsupported policy canary", 1, fmt.Errorf("unsupported binding type %s", step.Type)
	}
	if task.ID != "" {
		result.TaskID = task.ID
	}
	if task.DeploymentScope != "" {
		result.Scope = task.DeploymentScope
	}
	if code == 0 && (task.ExitCode == nil || *task.ExitCode != 0) {
		code, err = 1, fmt.Errorf("the canary receipt did not confirm a successful exit")
	}
	sanitizeBindingPolicyProbe(&result, step)
	if code == 0 {
		result.Status = "passed"
	}
	return result, title, code, err
}

// Policy reports contain stable stage statuses, not guest/provider details.
func sanitizeBindingPolicyProbe(result *bindingPolicyProbeResult, step bindingPolicyProbePlan) {
	if report := result.ServiceReport; report != nil {
		report.Service, report.URL = step.Name, "https://"+step.Name+".internal"
		report.TargetDeploymentID = step.TargetDeploymentID
		for _, check := range []*api.ServiceBindingProbeCheck{&report.DNS, &report.TLS, &report.Authorization, &report.Routing} {
			check.Status, check.Detail = bindingPolicyProbeStatus(check.Status), ""
		}
		if report.Error != "" {
			report.Error = "service binding canary did not succeed"
		}
	}
	if report := result.PostgresReport; report != nil {
		report.EnvironmentKey = step.Binding
		for _, check := range []*api.PostgresBindingProbeCheck{&report.Environment, &report.Configuration, &report.Connection, &report.Query} {
			check.Status, check.Detail = bindingPolicyProbeStatus(check.Status), ""
		}
		if report.Error != "" {
			report.Error = "PostgreSQL binding canary did not succeed"
		}
	}
	if report := result.Report; report != nil {
		report.Prefix = step.Prefix
		report.Mode = step.ProbeMode
		if report.Mode == "read" {
			report.Mode = ""
		}
		for _, check := range []*api.ObjectStorageBindingProbeCheck{&report.Environment, &report.Configuration, &report.Connection, &report.Authorization, &report.BucketAccess, report.Upload, report.Download, report.Cleanup} {
			if check != nil {
				check.Status, check.Detail = bindingPolicyProbeStatus(check.Status), ""
			}
		}
		if report.Error != "" {
			report.Error = "object-storage binding canary did not succeed"
		}
	}
	if report := result.OutboundReport; report != nil {
		for _, check := range []*api.OutboundBindingProbeCheck{&report.Configuration, &report.Identity, &report.Gateway, &report.Response} {
			check.Status, check.Detail = bindingPolicyProbeStatus(check.Status), ""
		}
	}
}

func bindingPolicyProbeStatus(status string) string {
	switch status {
	case "passed", "failed", "not_checked", "skipped":
		return status
	}
	return "unknown"
}

func renderBindingPolicyProbe(app string, result bindingPolicyProbeResult) {
	task := api.AppTaskResponse{ID: result.TaskID}
	switch {
	case result.ServiceReport != nil:
		task.DeploymentID = result.ServiceReport.DeploymentID
		renderServiceBindingProbeReport(app, *result.ServiceReport, task)
	case result.PostgresReport != nil:
		task.DeploymentID = result.PostgresReport.DeploymentID
		renderPostgresBindingProbeReport(app, *result.PostgresReport, task)
	case result.Report != nil:
		task.DeploymentID = result.Report.DeploymentID
		renderObjectStorageBindingProbeReport(app, *result.Report, task)
	case result.OutboundReport != nil:
		task.DeploymentID = result.OutboundReport.DeploymentID
		renderOutboundBindingProbeReport(app, *result.OutboundReport, task)
	default:
		_, _ = fmt.Fprintf(osStdout, "%s %s: %s resource=%s\n", result.Type, result.Name, result.Status, result.ResourceID)
	}
}
