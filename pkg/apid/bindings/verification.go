package bindings

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func NormalizeVerification(task state.BindingVerificationTask) *api.BindingVerification {
	result := &api.BindingVerification{TaskID: task.TaskID, Result: "unknown", Source: "task_guest", DeploymentID: task.DeploymentID,
		Scope: task.Scope, TargetDeploymentID: task.Pin.TargetDeploymentID, CheckedAt: task.FinishedAt, CredentialGeneration: task.Pin.CredentialGeneration}
	if task.Pin.ManagedService != nil {
		spec := *task.Pin.ManagedService
		result.ManagedService = &spec
	}
	if !state.AppTaskStatus(task.Status).Terminal() {
		result.Reason = "probe_pending"
		return result
	}
	if task.OutputTruncated || len(task.Stdout) > 4096 {
		result.Reason = "report_truncated"
		return result
	}
	switch task.Status {
	case string(state.AppTaskTimedOut):
		result.Result, result.Reason = "failed", "probe_timed_out"
		return result
	case string(state.AppTaskCancelled):
		result.Reason = "probe_cancelled"
		return result
	}
	var names, statuses []string
	reportFailed := false
	switch task.Pin.Type {
	case api.BindingTypeService:
		var report api.ServiceBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.Service != task.Pin.Binding || report.TargetDeploymentID != task.Pin.TargetDeploymentID {
			result.Reason = "report_invalid"
			return result
		}
		if task.Pin.ManagedService != nil && (report.ManagedService == nil || !report.ManagedService.Valid() || *report.ManagedService != *task.Pin.ManagedService || task.Pin.Revision != task.Pin.ManagedService.ContextRevision || task.DeploymentID != task.Pin.ManagedService.CallerDeploymentID || task.Scope != task.Pin.ManagedService.Scope) || task.Pin.ManagedService == nil && report.ManagedService != nil {
			result.Reason = "managed_service_report_invalid"
			return result
		}
		reportFailed = report.Error != "" || task.Pin.ManagedService != nil && (!report.Passed() || report.URL != "https://"+task.Pin.Binding+".internal")
		names = []string{"dns", "tls", "authorization", "routing"}
		statuses = []string{report.DNS.Status, report.TLS.Status, report.Authorization.Status, report.Routing.Status}
	case api.BindingTypePostgres:
		var report api.PostgresBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.EnvironmentKey != task.Pin.Binding {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"environment", "configuration", "connection", "query"}
		statuses = []string{report.Environment.Status, report.Configuration.Status, report.Connection.Status, report.Query.Status}
	case api.BindingTypeOutbound:
		var report api.OutboundBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.IntegrationID != task.Pin.Binding {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"configuration", "identity", "gateway", "response"}
		statuses = []string{report.Configuration.Status, report.Identity.Status, report.Gateway.Status, report.Response.Status}
	case api.BindingTypeObjectStorage:
		result.ObjectStorageMode = task.Pin.ObjectStorageMode
		if result.ObjectStorageMode == "" {
			result.ObjectStorageMode = "read"
		}
		var report api.ObjectStorageBindingProbeReport
		if json.Unmarshal([]byte(task.Stdout), &report) != nil || report.Prefix != task.Pin.Binding || report.Mode != task.Pin.ObjectStorageMode {
			result.Reason = "report_invalid"
			return result
		}
		reportFailed = report.Error != ""
		names = []string{"environment", "configuration", "connection", "authorization", "bucket_access"}
		statuses = []string{report.Environment.Status, report.Configuration.Status, report.Connection.Status, report.Authorization.Status, report.BucketAccess.Status}
		if task.Pin.ObjectStorageMode != "" {
			if !api.ValidObjectStorageWriteProbeMode(task.Pin.ObjectStorageMode) || report.Upload == nil || report.Download == nil || report.Cleanup == nil ||
				(task.Pin.ObjectStorageMode == "write" && report.Download.Status != "not_checked") {
				result.Reason = "report_invalid"
				return result
			}
			names = append(names, "upload", "cleanup")
			statuses = append(statuses, report.Upload.Status, report.Cleanup.Status)
			if task.Pin.ObjectStorageMode == "read_write" {
				names, statuses = append(names, "download"), append(statuses, report.Download.Status)
			}
		} else if report.Upload != nil || report.Download != nil || report.Cleanup != nil {
			result.Reason = "report_invalid"
			return result
		}
	default:
		result.Reason = "report_invalid"
		return result
	}
	passed := true
	for index, status := range statuses {
		if status != "passed" && status != "failed" && status != "not_checked" {
			result.Reason = "report_invalid"
			return result
		}
		result.Checks = append(result.Checks, api.BindingVerificationCheck{Name: names[index], Status: status})
		passed = passed && status == "passed"
	}
	result.Result, result.Reason = "failed", "check_failed"
	if passed && !reportFailed && task.Status == string(state.AppTaskSucceeded) && task.ExitCode != nil && *task.ExitCode == 0 {
		result.Result, result.Reason = "passed", ""
	}
	return result
}

func AppendVerificationIssue(inventory *api.AppBindingInventory) {
	issue := api.BindingInventoryIssue{Type: "verification", Code: "query_failed", Severity: "error", Message: "Binding verification metadata could not be read."}
	inventory.Issues = append(inventory.Issues, issue)
	inventory.Warnings = append(inventory.Warnings, issue.Message)
}
