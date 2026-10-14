package automationconsole

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

// Report settings patch the submitted definition, preserving inputs, retries and
// other advanced options. A different graph must be edited through JSON.
func reportShape(spec api.WorkflowSpec) bool {
	if spec.Trigger == nil || spec.Trigger.Type != "schedule" || len(spec.Steps) != 2 {
		return false
	}
	collect, deliver := spec.Steps[0], spec.Steps[1]
	return collect.Name == "collect" && deliver.Name == "deliver" &&
		len(collect.DependsOn) == 0 && len(deliver.DependsOn) == 1 && deliver.DependsOn[0] == "collect"
}

func reportForm(spec api.WorkflowSpec) *dashboard.AutomationReportForm {
	overlap := spec.Trigger.Overlap
	if overlap == "" {
		overlap = "skip"
	}
	timezone := spec.Trigger.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return &dashboard.AutomationReportForm{Schedule: spec.Trigger.Schedule, Timezone: timezone,
		CollectPath: spec.Steps[0].Path, DeliverPath: spec.Steps[1].Path,
		Overlap: overlap, CatchUp: spec.Trigger.ScheduleCatchUpPolicy(), CatchUpWindow: spec.Trigger.CatchUpWindow}
}

func prepareEditor(r *http.Request, data *dashboard.AutomationConsoleData) string {
	data.EditorMode = r.PostFormValue("editor_mode")
	if data.EditorMode == "" {
		data.EditorMode = "json"
	}
	if data.EditorMode == "json" {
		return ""
	}
	if data.EditorMode == "webhook" {
		return prepareWebhook(r, data)
	}
	if data.EditorMode != "report" {
		return "Choose starter settings or the JSON editor."
	}
	form := &dashboard.AutomationReportForm{
		Schedule: strings.TrimSpace(r.PostFormValue("report_schedule")), Timezone: strings.TrimSpace(r.PostFormValue("report_timezone")),
		CollectPath: strings.TrimSpace(r.PostFormValue("report_collect")), DeliverPath: strings.TrimSpace(r.PostFormValue("report_deliver")),
		Overlap: r.PostFormValue("report_overlap"), CatchUp: r.PostFormValue("report_catch_up"), CatchUpWindow: strings.TrimSpace(r.PostFormValue("report_window"))}
	data.Report = form
	var spec api.WorkflowSpec
	if decode(data.Definition, api.AutomationDefinitionMaxBytes, &spec) != nil || !reportShape(spec) {
		return "Report settings require the scheduled collect → deliver graph. Use the JSON editor for this definition."
	}
	spec.Name = data.Name
	previous := reportForm(spec)
	spec.Trigger.Schedule = form.Schedule
	// Rendering an omitted default must not turn an unchanged form submission
	// into a different draft, which would reject running saved cases on reopening.
	if form.Timezone != previous.Timezone {
		spec.Trigger.Timezone = form.Timezone
	}
	if form.Overlap != previous.Overlap {
		spec.Trigger.Overlap = form.Overlap
	}
	if form.CatchUp != previous.CatchUp {
		spec.Trigger.CatchUp = form.CatchUp
	}
	spec.Trigger.CatchUpWindow = form.CatchUpWindow
	spec.Steps[0].Path, spec.Steps[1].Path = form.CollectPath, form.DeliverPath
	if data.Version == 0 {
		enabled := false
		spec.Trigger.Enabled = &enabled
	}
	data.Definition = pretty(spec)
	return ""
}
