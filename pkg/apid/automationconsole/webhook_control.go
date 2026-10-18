package automationconsole

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) webhookAction(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	data.Ingress = &dashboard.AutomationWebhookSetup{EndpointID: r.PostFormValue("endpoint_id"), Pattern: r.PostFormValue("binding_event_type"),
		Filter: r.PostFormValue("binding_filter"), Submitted: true, ControlsDisabled: true}
	if failure := s.selectAutomation(r, scope, &data, data.Name); failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	endpoint, failure := s.scopedEndpoint(r, scope, data.Ingress.EndpointID)
	if failure != nil {
		s.fail(w, r, scope, data, *failure)
		return
	}
	operation, message := webhookOperation(r, data.Name, endpoint.ID)
	if message != "" {
		s.invalid(w, r, scope, data, message)
		return
	}
	if operation.Kind == "unbind-webhook" {
		var binding api.WebhookAutomationBindingResponse
		response := s.Host.Request(r, scope, Operation{Kind: "webhook-binding", EndpointID: endpoint.ID})
		if !read(response, &binding) || binding.EndpointID != endpoint.ID || binding.WorkflowName != data.Name {
			s.invalid(w, r, scope, data, "Reload the binding for this automation before removing it.")
			return
		}
	}
	response := s.Host.Request(r, scope, operation)
	if !success(response) {
		s.fail(w, r, scope, data, response)
		return
	}
	query := url.Values{"name": {data.Name}, "endpoint": {endpoint.ID}}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(scope.Slug)+"/automations?"+query.Encode()+"#webhook-setup", http.StatusSeeOther)
}

func webhookOperation(r *http.Request, name, endpointID string) (Operation, string) {
	action := r.PathValue("action")
	if r.PostFormValue("confirm_"+action) != "yes" {
		return Operation{}, "Confirm how future webhook deliveries will be routed before submitting."
	}
	version, err := strconv.ParseInt(r.PostFormValue("binding_version"), 10, 64)
	if err != nil || version < 0 || action == "unbind-webhook" && version == 0 {
		return Operation{}, "Reload the endpoint to read its current binding version."
	}
	operation := Operation{Kind: action, Name: name, EndpointID: endpointID, Version: version}
	if action == "unbind-webhook" {
		return operation, ""
	}
	key := r.PostFormValue("action_key")
	if _, err := uuid.Parse(key); err != nil {
		return Operation{}, "Reload the endpoint before saving its binding."
	}
	var filter json.RawMessage
	if decode(r.PostFormValue("binding_filter"), api.WorkflowWebhookFilterMaxBytes, &filter) != nil {
		return Operation{}, "Enter valid binding-filter JSON; use {} to accept all matching event data."
	}
	operation.Key = key
	operation.Body = api.PutWebhookAutomationBindingRequest{ExpectedVersion: &version, WorkflowName: name,
		EventType: r.PostFormValue("binding_event_type"), Filter: filter, TakeOverDelivery: true}
	return operation, ""
}
