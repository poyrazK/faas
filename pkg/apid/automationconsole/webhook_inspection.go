package automationconsole

import (
	"context"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) inspectIngress(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) *Response {
	if data.Name == "" || data.Webhook == nil && !data.Published && data.Ingress == nil && r.URL.Query().Get("endpoint") == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowAutomationHealthReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if data.Ingress == nil {
		data.Ingress = &dashboard.AutomationWebhookSetup{EndpointID: r.URL.Query().Get("endpoint"), EventID: r.URL.Query().Get("event_id")}
	}
	view := data.Ingress
	view.ActionKey = uuid.NewString()
	var endpoints []api.InboundWebhookEndpointResponse
	if read(s.Host.Request(r, scope, Operation{Kind: "endpoints"}), &endpoints) {
		for _, endpoint := range endpoints {
			if validEndpoint(scope, endpoint, endpoint.ID) {
				item := endpointView(endpoint)
				item.URL = consoleURL(scope.Slug, data.Name, "") + "&endpoint=" + url.QueryEscape(item.ID) + "#webhook-setup"
				view.Endpoints = append(view.Endpoints, item)
			}
		}
	} else {
		view.EndpointsError = "Inbound webhook endpoints are temporarily unavailable."
	}
	if view.EndpointID == "" {
		return nil
	}
	endpoint, failure := s.scopedEndpoint(r, scope, view.EndpointID)
	if failure != nil {
		return failure
	}
	view.EndpointID = endpoint.ID
	item := endpointView(endpoint)
	view.Endpoint = &item
	s.inspectBinding(r, scope, data)
	return s.inspectWebhookReceipt(r, scope, data)
}

func validEndpoint(scope Scope, endpoint api.InboundWebhookEndpointResponse, id string) bool {
	return endpoint.ID == id && endpoint.AppID == scope.AppID && endpoint.AccountID == scope.AccountID &&
		(endpoint.Provider == "stripe" || endpoint.Provider == "generic")
}

func endpointView(endpoint api.InboundWebhookEndpointResponse) dashboard.AutomationInboundEndpoint {
	return dashboard.AutomationInboundEndpoint{ID: endpoint.ID, Name: endpoint.Name, Provider: endpoint.Provider,
		DeliveryPath: endpoint.DeliveryPath, Enabled: endpoint.Enabled}
}

func (s *Service) scopedEndpoint(r *http.Request, scope Scope, id string) (api.InboundWebhookEndpointResponse, *Response) {
	var endpoint api.InboundWebhookEndpointResponse
	_, err := uuid.Parse(id)
	if err != nil {
		response := Response{Status: http.StatusNotFound}
		return endpoint, &response
	}
	// Endpoint IDs may be compact UUIDs. Preserve the API's opaque identity.
	response := s.Host.Request(r, scope, Operation{Kind: "endpoint", EndpointID: id})
	if !read(response, &endpoint) {
		if success(response) {
			response = badResponse()
		}
		return endpoint, &response
	}
	if !validEndpoint(scope, endpoint, id) {
		response = Response{Status: http.StatusNotFound}
		return api.InboundWebhookEndpointResponse{}, &response
	}
	return endpoint, nil
}

func (s *Service) inspectBinding(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	view := data.Ingress
	var binding api.WebhookAutomationBindingResponse
	response := s.Host.Request(r, scope, Operation{Kind: "webhook-binding", EndpointID: view.EndpointID})
	if read(response, &binding) && binding.EndpointID == view.EndpointID {
		view.Binding, view.BindingVersion, view.BindingObserved = &binding, binding.Version, true
	} else if response.Status == http.StatusNotFound {
		view.BindingObserved = true
	} else {
		view.BindingError = "The current automation binding is temporarily unavailable. Reload before changing it."
	}
	if view.Submitted {
		return
	}
	view.Pattern, view.Filter = "*", "{}"
	if data.Webhook != nil {
		view.Pattern, view.Filter = data.Webhook.EventType, data.Webhook.Filter
	}
	if view.Binding != nil {
		view.Pattern, view.Filter = view.Binding.EventType, pretty(view.Binding.Filter)
	}
}

func (s *Service) inspectWebhookReceipt(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) *Response {
	view := data.Ingress
	if view.EventID == "" {
		return nil
	}
	if !api.ValidInboundWebhookEventID(view.EventID) {
		view.ReceiptError = "Enter a printable provider event ID of at most 256 bytes."
		return nil
	}
	var receipt api.WebhookAutomationReceiptResponse
	response := s.Host.Request(r, scope, Operation{Kind: "webhook-receipt", EndpointID: view.EndpointID, EventID: view.EventID})
	if !read(response, &receipt) {
		view.ReceiptError = "The automation receipt is temporarily unavailable."
		if response.Status == http.StatusNotFound {
			view.ReceiptError = "No automation receipt was found for this endpoint and provider event ID."
		}
		return nil
	}
	if receipt.EndpointID != view.EndpointID || receipt.ProviderEventID != view.EventID || receipt.WorkflowName != data.Name {
		response = Response{Status: http.StatusNotFound}
		return &response
	}
	view.Receipt = &receipt
	if receipt.RunID != "" {
		if _, failure := s.scopedRun(r, scope, data.Name, receipt.RunID); failure == nil {
			view.RunURL = consoleURL(scope.Slug, data.Name, receipt.RunID)
		} else {
			view.ReceiptError = "The receipt's run could not be verified for this automation."
		}
	}
	return nil
}
