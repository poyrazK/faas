package automationconsole

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) setup(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	if data.Name == "" {
		return
	}
	spec, err := definition(*data)
	data.ScheduleConfigured = err == nil && spec.Trigger != nil && spec.Trigger.Type == "schedule"
	if err == nil && reportShape(spec) && data.Report == nil {
		data.Report = reportForm(spec)
		if data.EditorMode == "" {
			data.EditorMode = "report"
		}
	}
	if err == nil && webhookShape(spec) && data.Webhook == nil {
		data.Webhook = webhookForm(spec)
		if data.EditorMode == "" {
			data.EditorMode = "webhook"
		}
	}
	data.SetupChecks = setupChecks(*data)
	if !data.PublishedSchedule {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowSchedulePreviewReadTimeout)
	defer cancel()
	var preview api.WorkflowSchedulePreviewResponse
	if read(s.Host.Request(r.WithContext(ctx), scope, Operation{Kind: "schedule-preview", Name: data.Name}), &preview) && preview.WorkflowName == data.Name {
		data.PublishedSchedulePreview = &preview
	} else {
		data.PublishedScheduleError = "Published schedule preview is temporarily unavailable."
	}
}

func setupChecks(data dashboard.AutomationConsoleData) []dashboard.AutomationSetupCheck {
	checks := []dashboard.AutomationSetupCheck{
		{Name: "Workflow runtime", Status: "Unavailable", Detail: "Runtime availability could not be read."},
		{Name: "Execution prerequisites", Status: "Unverified", Detail: "App eligibility and its live default deployment could not be confirmed."},
		{Name: "Handler routes", Status: "Unverified", Detail: "Deploy and test the routes in your definition. Validation and mocked scenarios do not call handlers or verify credentials."},
		{Name: "Publication", Status: "Draft only", Detail: "Save the definition, run its checks and publish the saved revision."},
		{Name: "Automatic starts", Status: "Paused", Detail: "New guided report and webhook starters stay paused after publishing. Enable automatic starts explicitly when ready."},
	}
	if data.RuntimeObserved {
		checks[0].Status, checks[0].Detail = "Disabled", "The workflow runtime is disabled. Draft preparation and simulation are still available."
		if data.RuntimeEnabled {
			checks[0].Status, checks[0].Detail = "Available", "The workflow runtime is enabled. Each real start still passes admission checks."
		}
		if data.UnavailableReason == "" && data.RuntimeEnabled {
			checks[1].Status, checks[1].Detail = "Available", "App eligibility and a live default deployment were confirmed by the automation API."
		} else if detail := unavailableDetail(data.UnavailableReason); detail != "" {
			checks[1].Status, checks[1].Detail = "Blocked", detail
		}
	}
	if data.Published {
		checks[3].Status, checks[3].Detail = "Published", "A published revision exists. Unsaved edits and newer drafts do not change it."
	}
	if data.FailurePaused {
		checks[4].Detail = "Starts are paused after failures. Review the failure policy and run diagnosis."
	} else if data.Published && data.Enabled {
		checks[4].Status, checks[4].Detail = "Enabled", "Automatic starts are enabled for the published definition, subject to runtime admission."
	}
	if !data.Published {
		checks[4].Status, checks[4].Detail = "Not published", "This draft has no automatic starts. New guided report and webhook starters stay paused when first published."
	}
	if !data.PublicationObserved {
		checks[3].Status, checks[3].Detail = "Unavailable", "Current publication metadata could not be read. Reload before publishing or changing automatic starts."
		checks[4].Status, checks[4].Detail = "Unavailable", "The current automatic-start setting could not be confirmed."
	}
	return checks
}

func unavailableDetail(reason string) string {
	switch reason {
	case "runtime_disabled":
		return "Execution checks are blocked while the workflow runtime is disabled; deployment readiness remains unverified."
	case "account_inactive":
		return "The app's account is inactive. Deployment readiness remains unverified."
	case "plan_not_allowed":
		return "The account's plan does not allow workflow execution. Deployment readiness remains unverified."
	case "maintenance":
		return "The app is in maintenance. Deployment readiness remains unverified."
	case "tenant_required":
		return "This app requires a tenant-scoped workflow configuration."
	case "deployment_unavailable":
		return "No live default deployment is available. Deploy your handlers before running this automation."
	default:
		return ""
	}
}

func (s *Service) previewSchedule(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	spec, err := definition(data)
	if err != nil || spec.Trigger == nil || spec.Trigger.Type != "schedule" {
		s.invalid(w, r, scope, data, "Enter a valid definition with a schedule trigger before previewing.")
		return
	}
	response := s.Host.Request(r, scope, Operation{Kind: "validate", Body: api.ValidateAutomationRequest{Definition: spec}})
	var validation api.ValidateAutomationResponse
	if !read(response, &validation) {
		s.fail(w, r, scope, data, response)
		return
	}
	data.Validation = &validation
	if validation.Valid {
		now := time.Now().UTC()
		preview, err := api.BuildWorkflowSchedulePreview(api.WorkflowSchedulePreviewInput{
			WorkflowName: spec.Name, Trigger: *spec.Trigger, ObservedAt: now, EvaluationAt: now,
			Enabled: spec.Trigger.Enabled == nil || *spec.Trigger.Enabled})
		if err != nil {
			s.invalid(w, r, scope, data, "The schedule could not be previewed. Check its cron, timezone and missed-start settings.")
			return
		}
		data.DraftSchedule = &preview
	}
	s.render(w, r, scope, data, http.StatusOK)
}
