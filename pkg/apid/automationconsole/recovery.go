package automationconsole

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

// Resolve both app and automation identity before any run-specific read or write.
func (s *Service) scopedRun(r *http.Request, scope Scope, name, id string) (api.WorkflowRunResponse, *Response) {
	var run api.WorkflowRunResponse
	if _, err := uuid.Parse(id); err != nil || name == "" {
		response := Response{Status: http.StatusNotFound}
		return run, &response
	}
	response := s.Host.Request(r, scope, Operation{Kind: "get-run", RunID: id})
	if !read(response, &run) {
		if success(response) {
			response = badResponse()
		}
		return run, &response
	}
	if run.ID != id || run.AppID != scope.AppID || run.WorkflowName != name {
		response = Response{Status: http.StatusNotFound}
		return api.WorkflowRunResponse{}, &response
	}
	return run, nil
}

func (s *Service) inspectRecovery(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData, id string) {
	var diagnostics api.WorkflowRunDiagnosticsResponse
	response := s.Host.Request(r, scope, Operation{Kind: "diagnostics", RunID: id})
	if read(response, &diagnostics) && diagnostics.RunID == id && diagnostics.WorkflowName == data.Name {
		data.Diagnostics = &diagnostics
		data.StateExplanation = diagnosticExplanation(diagnostics.StateReason)
		data.Steps = nil
		for _, step := range diagnostics.Steps {
			data.Steps = append(data.Steps, dashboard.AutomationConsoleStep{Name: step.StepName, Status: step.Status,
				Kind: step.Kind, Attempt: step.Attempt, RetryBase: step.RetryBase, NextRetryAt: step.NextRetryAt, NextCheckAt: step.NextCheckAt})
		}
	} else {
		data.DiagnosticsError = "Run diagnostics are temporarily unavailable. Refresh before attempting recovery."
	}
	var history api.ListWorkflowResumesResponse
	response = s.Host.Request(r, scope, Operation{Kind: "resumes", RunID: id})
	if !read(response, &history) {
		data.RecoveryHistoryError = "Recovery history is temporarily unavailable."
		return
	}
	for _, item := range history.Resumes {
		if item.RunID != id {
			data.RecoveryHistory, data.RecoveryHistoryError = nil, "Recovery history is temporarily unavailable."
			return
		}
		data.RecoveryHistory = append(data.RecoveryHistory, dashboard.AutomationConsoleResume{Number: item.ResumeNumber,
			PreviousStatus: item.PreviousStatus, CreatedAt: item.CreatedAt, Steps: item.ResumedSteps})
	}
}

func diagnosticExplanation(reason string) string {
	switch reason {
	case api.AutomationQueueReady:
		return "Ready for dispatch admission. A worker may still need to become available."
	case api.AutomationQueueScheduled:
		return "Waiting for its next scheduled wake."
	case api.AutomationQueueRetryBackoff:
		return "Waiting for the retry delay before another attempt."
	case api.AutomationQueueParkedWait, "awaiting_event":
		return "Waiting for a timer, event or callback. Review the step kind and next check below."
	case api.AutomationQueueAppCapacity:
		return "Waiting for execution capacity in this app."
	case api.AutomationQueueTenantCapacity:
		return "Waiting for the tenant's execution capacity."
	case api.AutomationQueueWorkflowCapacity:
		return "Waiting for this automation's execution capacity."
	case "running":
		return "A worker holds this run."
	case "failed", "dead":
		return "The run stopped after a failure. Review recovery eligibility and blockers below."
	case "cancelled":
		return "Cancellation was requested. Completed effects are not undone; active work may need time to stop."
	case "succeeded":
		return "The run completed successfully."
	default:
		return "Review the observed state and step details below."
	}
}
