package automationconsole

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func webhookShape(spec api.WorkflowSpec) bool {
	if spec.Trigger == nil || spec.Trigger.Type != "event" || len(spec.Steps) != 2 {
		return false
	}
	lookup, enrich := spec.Steps[0], spec.Steps[1]
	return lookup.Name == "lookup" && enrich.Name == "enrich" && len(lookup.DependsOn) == 0 &&
		len(enrich.DependsOn) == 1 && enrich.DependsOn[0] == "lookup"
}

func webhookForm(spec api.WorkflowSpec) *dashboard.AutomationWebhookForm {
	filter := "{}"
	if len(spec.Trigger.Filter) != 0 {
		filter = pretty(spec.Trigger.Filter)
	}
	return &dashboard.AutomationWebhookForm{Source: spec.Trigger.Source, EventType: spec.Trigger.EventType,
		Filter: filter, LookupPath: spec.Steps[0].Path, EnrichPath: spec.Steps[1].Path}
}

func prepareWebhook(r *http.Request, data *dashboard.AutomationConsoleData) string {
	form := &dashboard.AutomationWebhookForm{Source: strings.TrimSpace(r.PostFormValue("webhook_source")),
		EventType: strings.TrimSpace(r.PostFormValue("webhook_event_type")), Filter: r.PostFormValue("webhook_filter"),
		LookupPath: strings.TrimSpace(r.PostFormValue("webhook_lookup")), EnrichPath: strings.TrimSpace(r.PostFormValue("webhook_enrich"))}
	data.Webhook = form
	var spec api.WorkflowSpec
	if decode(data.Definition, api.AutomationDefinitionMaxBytes, &spec) != nil || !webhookShape(spec) {
		return "Webhook settings require the event lookup → enrich graph. Use the JSON editor for this definition."
	}
	var filter json.RawMessage
	if decode(form.Filter, api.WorkflowRunInputMaxBytes, &filter) != nil {
		return "Enter valid event-filter JSON; use {} to accept all matching event data."
	}
	spec.Name = data.Name
	spec.Trigger.Source, spec.Trigger.EventType, spec.Trigger.Filter = form.Source, form.EventType, filter
	spec.Steps[0].Path, spec.Steps[1].Path = form.LookupPath, form.EnrichPath
	if data.Version == 0 {
		enabled := false
		spec.Trigger.Enabled = &enabled
	}
	data.Definition = pretty(spec)
	return ""
}
