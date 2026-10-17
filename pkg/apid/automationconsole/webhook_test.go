// adr: 1387
package automationconsole

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestWebhookEditorPreservesAdvancedSettingsAndPausesNewStarts(t *testing.T) {
	bundle, err := automationtemplates.Load("webhook-enrichment")
	if err != nil {
		t.Fatal(err)
	}
	bundle.Definition.MaxConcurrentRuns = 2
	data := dashboard.AutomationConsoleData{Name: "my-orders", Definition: pretty(bundle.Definition)}
	form := url.Values{"editor_mode": {"webhook"}, "webhook_source": {"external.shop"}, "webhook_event_type": {"order.*"},
		"webhook_filter": {`{"data":{"region":"eu"}}`}, "webhook_lookup": {"/orders/lookup"}, "webhook_enrich": {"/orders/record"}}
	if message := prepareEditor(reportRequest(form), &data); message != "" {
		t.Fatal(message)
	}
	spec, err := definition(data)
	if err != nil || spec.Name != data.Name || spec.Trigger.Source != "external.shop" || spec.Trigger.EventType != "order.*" ||
		spec.Steps[0].Path != "/orders/lookup" || spec.Steps[1].Path != "/orders/record" || spec.MaxConcurrentRuns != 2 ||
		pretty(spec.Steps[0].Input) != pretty(bundle.Definition.Steps[0].Input) || spec.Trigger.Enabled == nil || *spec.Trigger.Enabled {
		t.Fatalf("webhook configuration lost settings: %s (%v)", data.Definition, err)
	}
	data.Version = 2
	enabled := true
	spec.Trigger.Enabled = &enabled
	data.Definition = pretty(spec)
	if message := prepareEditor(reportRequest(form), &data); message != "" {
		t.Fatal(message)
	}
	spec, err = definition(data)
	if err != nil || !*spec.Trigger.Enabled {
		t.Fatal("editing an existing webhook changed activation")
	}
	form.Set("webhook_filter", "{invalid")
	if message := prepareEditor(reportRequest(form), &data); message == "" {
		t.Fatal("malformed filter accepted")
	}
	form.Set("webhook_filter", "{}")
	spec.Steps = spec.Steps[:1]
	data.Definition = pretty(spec)
	if message := prepareEditor(reportRequest(form), &data); message == "" {
		t.Fatal("webhook form replaced a custom graph")
	}
}

func webhookInspectionFixture(t *testing.T) (*inspectionHost, string) {
	t.Helper()
	host, _ := inspectionFixture(t)
	bundle, err := automationtemplates.Load("webhook-enrichment")
	if err != nil {
		t.Fatal(err)
	}
	bundle.Definition.Name = "orders"
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	endpoint := api.InboundWebhookEndpointResponse{ID: id, AppID: "app", AccountID: "account", Name: "signed-orders", Provider: "generic",
		SigningSecretMasked: "PRIVATE-ENDPOINT-SECRET", EndpointURL: "PRIVATE-ENDPOINT-TOKEN"}
	host.responses["get"] = inspectionJSON(t, api.AutomationResponse{Name: "orders", Version: 2, Draft: bundle.Definition, Published: &bundle.Definition})
	host.responses["endpoints"] = inspectionJSON(t, []api.InboundWebhookEndpointResponse{endpoint})
	host.responses["endpoint"] = inspectionJSON(t, endpoint)
	host.responses["webhook-binding"] = Response{Status: http.StatusNotFound}
	host.responses["webhook-receipt"] = inspectionJSON(t, api.WebhookAutomationReceiptResponse{
		EndpointID: id, ProviderEventID: "event-1", WorkflowName: "orders", Status: "accepted", RoutingStatus: "filtered"})
	return host, id
}

func webhookShow(t *testing.T, host *inspectionHost, query string) *httptest.ResponseRecorder {
	t.Helper()
	service := Service{Host: host, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	service.Show(response, httptest.NewRequest(http.MethodGet, "/?name=orders"+query, nil))
	return response
}

func TestWebhookInspectionPrivacyFilteringAndUnavailableBinding(t *testing.T) {
	host, id := webhookInspectionFixture(t)
	response := webhookShow(t, host, "&endpoint="+id+"&event_id=event-1")
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "filtered") || strings.Contains(body, "PRIVATE-") ||
		!strings.Contains(body, "does not report retry counts") {
		t.Fatalf("receipt inspection: %d %s", response.Code, body)
	}
	for _, call := range host.calls {
		if call.Kind == "bind-webhook" || call.Kind == "unbind-webhook" || call.Kind == "run" {
			t.Fatal("inspection mutated routing or started work")
		}
	}
	host.responses["webhook-binding"] = Response{Status: http.StatusServiceUnavailable}
	response = webhookShow(t, host, "&endpoint="+id)
	if !strings.Contains(response.Body.String(), "current automation binding is temporarily unavailable") ||
		strings.Contains(response.Body.String(), "No automation binding is configured") ||
		!strings.Contains(response.Body.String(), `disabled>Save webhook binding`) {
		t.Fatal("unavailable binding looked absent or editable")
	}
}

func TestWebhookInspectionScopesEndpointReceiptAndRun(t *testing.T) {
	for _, invalid := range []string{"endpoint", "receipt", "run"} {
		t.Run(invalid, func(t *testing.T) {
			host, id := webhookInspectionFixture(t)
			if invalid == "endpoint" {
				host.responses["endpoint"] = inspectionJSON(t, api.InboundWebhookEndpointResponse{ID: id, AppID: "foreign", AccountID: "account", Provider: "generic"})
			} else {
				receipt := api.WebhookAutomationReceiptResponse{EndpointID: id, ProviderEventID: "event-1", WorkflowName: "orders", RunID: uuid.NewString()}
				if invalid == "receipt" {
					receipt.WorkflowName = "foreign"
				} else {
					host.responses["get-run"] = inspectionJSON(t, api.WorkflowRunResponse{ID: receipt.RunID, AppID: "foreign", WorkflowName: "orders"})
				}
				host.responses["webhook-receipt"] = inspectionJSON(t, receipt)
			}
			response := webhookShow(t, host, "&endpoint="+id+"&event_id=event-1")
			if invalid != "run" && response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), ">Inspect webhook run</a>") {
				t.Fatal("foreign endpoint, receipt or run was exposed")
			}
			for _, call := range host.calls {
				if invalid == "endpoint" && (call.Kind == "webhook-binding" || call.Kind == "webhook-receipt") || invalid != "run" && call.Kind == "get-run" {
					t.Fatal("read proceeded before endpoint/automation scope checks")
				}
			}
		})
	}
}

func TestWebhookRoutingChangesRequireConfirmationVersionAndKey(t *testing.T) {
	for _, test := range []struct{ field, value string }{{"confirm_bind-webhook", ""}, {"binding_version", ""},
		{"binding_version", "-1"}, {"action_key", "invalid"}, {"binding_filter", "{"}} {
		form := url.Values{"confirm_bind-webhook": {"yes"}, "binding_version": {"0"}, "action_key": {uuid.NewString()}, "binding_filter": {"{}"}}
		form.Set(test.field, test.value)
		r := reportRequest(form)
		r.SetPathValue("action", "bind-webhook")
		if _, message := webhookOperation(r, "orders", uuid.NewString()); message == "" {
			t.Fatalf("invalid %s accepted", test.field)
		}
	}
	form := url.Values{"confirm_bind-webhook": {"yes"}, "binding_version": {"3"}, "action_key": {uuid.NewString()},
		"binding_filter": {`{"data":{"region":"eu"}}`}, "binding_event_type": {"order.*"}}
	r := reportRequest(form)
	r.SetPathValue("action", "bind-webhook")
	op, message := webhookOperation(r, "orders", "endpoint")
	body, ok := op.Body.(api.PutWebhookAutomationBindingRequest)
	if message != "" || !ok || *body.ExpectedVersion != 3 || body.WorkflowName != "orders" || !body.TakeOverDelivery ||
		!json.Valid(body.Filter) || body.EventType != "order.*" || op.Key != form.Get("action_key") {
		t.Fatal("binding did not retain its scope, version, filter or key")
	}
}
