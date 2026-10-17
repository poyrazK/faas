// adr: 1387
package apidserver_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/state"
)

const consoleWebhookSecret = "PRIVATE-CONSOLE-WEBHOOK-SECRET-32-BYTES"

func consoleWebhookEndpoint(t *testing.T, e apidserver.TestEnv, slug string) api.InboundWebhookEndpointResponse {
	t.Helper()
	response := e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/inbound-webhooks", api.CreateInboundWebhookEndpointRequest{
		Name: "signed-orders", Provider: "generic", SigningSecret: consoleWebhookSecret, DeliveryPath: "/incoming-orders"}, nil)
	var endpoint api.InboundWebhookEndpointResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &endpoint) != nil {
		t.Fatalf("endpoint: %d %s", response.Code, response.Body.String())
	}
	return endpoint
}

func consoleWebhookForm(t *testing.T, name string) url.Values {
	t.Helper()
	bundle, err := automationtemplates.Load("webhook-enrichment")
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := json.Marshal(bundle.Definition)
	sample, _ := json.Marshal(bundle.Sample)
	scenarios, _ := json.Marshal(bundle.Scenarios)
	return url.Values{"name": {name}, "definition": {string(definition)}, "sample": {string(sample)}, "scenarios": {string(scenarios)},
		"expected_version": {"0"}, "action_key": {uuid.NewString()}, "editor_mode": {"webhook"}, "webhook_source": {"external.orders"},
		"webhook_event_type": {"order.created"}, "webhook_filter": {"{}"}, "webhook_lookup": {"/orders/lookup"}, "webhook_enrich": {"/orders/record"}}
}

func consoleWebhookIngress(t *testing.T, e apidserver.TestEnv, endpoint api.InboundWebhookEndpointResponse, eventID, eventType, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"customer_id":"cus_1","order_id":"ord_1","private":"PRIVATE-WEBHOOK-BODY"}`)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stamp + "\n" + eventID + "\n" + eventType + "\n"))
	_, _ = mac.Write(body)
	endpointURL, err := url.Parse(endpoint.EndpointURL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, endpointURL.Path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(api.InboundWebhookEventIDHeader, eventID)
	request.Header.Set(api.InboundWebhookEventTypeHeader, eventType)
	request.Header.Set(api.InboundWebhookTimestampHeader, stamp)
	request.Header.Set(api.InboundWebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, request)
	return response
}

func TestAutomationConsoleWebhookAuthorBindAndInspectJourney(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e, app, cookie := consoleFixture(t)
	endpoint := consoleWebhookEndpoint(t, e, app.Slug)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=webhook-enrichment")
	form := consoleWebhookForm(t, "orders")
	for _, action := range []string{"validate", "simulate", "save"} {
		page = consolePost(e, cookie, page, base+"/"+action, form)
		if page.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, page.Code, page.Body.String())
		}
	}
	saved := consoleVersion(t, e, app.Slug, "orders")
	if saved.Draft.Steps[0].Path != "/orders/lookup" || saved.Draft.Trigger.Enabled == nil || *saved.Draft.Trigger.Enabled {
		t.Fatal("guided webhook configuration was lost or activated")
	}
	form.Set("expected_version", strconv.FormatInt(saved.Version, 10))
	definition, _ := json.Marshal(saved.Draft)
	form.Set("definition", string(definition))
	page = consolePost(e, cookie, page, base+"/publish", form)
	published := consoleVersion(t, e, app.Slug, "orders")
	if page.Code != http.StatusOK || published.Published == nil || published.Enabled {
		t.Fatalf("publication activated starts: %d %s", page.Code, page.Body.String())
	}
	selected := base + "?name=orders&endpoint=" + endpoint.ID
	page = apidserver.RestartProgressDashboardGET(e, cookie, selected)
	bind := url.Values{"name": {"orders"}, "endpoint_id": {endpoint.ID}, "binding_version": {"0"}, "action_key": {uuid.NewString()},
		"binding_event_type": {"order.created"}, "binding_filter": {"{}"}}
	missing := consolePost(e, cookie, page, base+"/bind-webhook", bind)
	if missing.Code != http.StatusBadRequest {
		t.Fatal("binding did not require routing confirmation")
	}
	bind.Set("confirm_bind-webhook", "yes")
	bound := consolePost(e, cookie, page, base+"/bind-webhook", bind)
	replay := consolePost(e, cookie, page, base+"/bind-webhook", bind)
	if bound.Code != http.StatusSeeOther || replay.Code != http.StatusSeeOther || bound.Header().Get("Location") != replay.Header().Get("Location") {
		t.Fatalf("binding/replay: %d %d %s", bound.Code, replay.Code, bound.Body.String())
	}
	binding, err := (*e.Store()).GetWebhookAutomationBinding(t.Context(), endpoint.ID)
	if err != nil || binding.WorkflowName != "orders" {
		t.Fatal("binding not retained")
	}
	bind.Set("action_key", uuid.NewString())
	stale := consolePost(e, cookie, page, base+"/bind-webhook", bind)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "disabled>Save webhook binding") {
		t.Fatal("stale binding was overwritten or its controls stayed enabled")
	}
	form.Set("expected_version", strconv.FormatInt(published.Version, 10))
	form.Set("enabled", "true")
	enabled := consolePost(e, cookie, page, base+"/enabled", form)
	if enabled.Code != http.StatusSeeOther {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	bad := consoleWebhookIngress(t, e, endpoint, "bad-event", "order.created", "wrong-secret")
	if bad.Code == http.StatusAccepted {
		t.Fatal("bad signature was accepted")
	}
	accepted := consoleWebhookIngress(t, e, endpoint, "order-event", "order.created", consoleWebhookSecret)
	duplicate := consoleWebhookIngress(t, e, endpoint, "order-event", "order.created", consoleWebhookSecret)
	var receipt api.WebhookAutomationReceiptResponse
	if accepted.Code != http.StatusAccepted || duplicate.Code != http.StatusAccepted || json.Unmarshal(duplicate.Body.Bytes(), &receipt) != nil || !receipt.Duplicate {
		t.Fatalf("signed ingress/deduplication: %d %d %s", accepted.Code, duplicate.Code, duplicate.Body.String())
	}
	work, err := (*e.Store()).ClaimDuePublishedEvent(t.Context(), time.Now().UTC())
	if err != nil || len(work.RecipientSnapshot) != 1 {
		t.Fatalf("captured webhook work: %+v %v", work, err)
	}
	runID, err := (*e.Store()).AdmitEventWorkflow(t.Context(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, selected+"&event_id=order-event")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Inspect webhook run") || !strings.Contains(page.Body.String(), runID) {
		t.Fatalf("receipt: %d %s", page.Code, page.Body.String())
	}
	for _, private := range []string{consoleWebhookSecret, endpoint.EndpointURL, "PRIVATE-WEBHOOK-BODY"} {
		if strings.Contains(page.Body.String(), private) {
			t.Fatal("webhook inspection exposed secret or payload data")
		}
	}
	ignored := consoleWebhookIngress(t, e, endpoint, "wrong-type", "order.refunded", consoleWebhookSecret)
	if ignored.Code != http.StatusAccepted {
		t.Fatal("nonmatching event was not receipted")
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, selected+"&event_id=wrong-type")
	if !strings.Contains(page.Body.String(), "event_filtered") {
		t.Fatal("ignored receipt was not explained")
	}
	remove := url.Values{"name": {"orders"}, "endpoint_id": {endpoint.ID}, "binding_version": {strconv.FormatInt(binding.Version, 10)}, "confirm_unbind-webhook": {"yes"}}
	removed := consolePost(e, cookie, page, base+"/unbind-webhook", remove)
	if removed.Code != http.StatusSeeOther {
		t.Fatalf("unbind: %d %s", removed.Code, removed.Body.String())
	}
	if _, err := (*e.Store()).GetWebhookAutomationBinding(t.Context(), endpoint.ID); err == nil {
		t.Fatal("binding was not removed")
	}
}

func TestAutomationConsoleWebhookOwnershipCSRFAndPausedReceipts(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e, app, cookie := consoleFixture(t)
	endpoint := consoleWebhookEndpoint(t, e, app.Slug)
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=webhook-enrichment")
	form := consoleWebhookForm(t, "orders")
	page = consolePost(e, cookie, page, base+"/save", form)
	saved := consoleVersion(t, e, app.Slug, "orders")
	definition, _ := json.Marshal(saved.Draft)
	form.Set("definition", string(definition))
	form.Set("expected_version", strconv.FormatInt(saved.Version, 10))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", page.Code, page.Body.String())
	}
	bind := url.Values{"name": {"orders"}, "endpoint_id": {endpoint.ID}, "binding_version": {"0"}, "action_key": {uuid.NewString()},
		"binding_event_type": {"order.*"}, "binding_filter": {"{}"}, "confirm_bind-webhook": {"yes"}}
	if response := consolePost(e, cookie, httptest.NewRecorder(), base+"/bind-webhook", bind); response.Code != http.StatusBadRequest {
		t.Fatal("missing CSRF was accepted")
	}
	other, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "foreign-webhook", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	foreign := consoleWebhookEndpoint(t, e, other.Slug)
	for _, path := range []string{base + "?name=orders&endpoint=" + foreign.ID, base + "?name=orders&endpoint=" + foreign.ID + "&event_id=private"} {
		if got := apidserver.RestartProgressDashboardGET(e, cookie, path); got.Code != http.StatusNotFound {
			t.Fatal("foreign endpoint read accepted")
		}
	}
	bind.Set("endpoint_id", foreign.ID)
	if response := consolePost(e, cookie, page, base+"/bind-webhook", bind); response.Code != http.StatusNotFound {
		t.Fatal("foreign endpoint binding accepted")
	}
	bind.Set("endpoint_id", endpoint.ID)
	if response := consolePost(e, cookie, page, base+"/bind-webhook", bind); response.Code != http.StatusSeeOther {
		t.Fatalf("bind: %d %s", response.Code, response.Body.String())
	}
	ignored := consoleWebhookIngress(t, e, endpoint, "paused-event", "order.created", consoleWebhookSecret)
	if ignored.Code != http.StatusAccepted {
		t.Fatalf("paused receipt: %d %s", ignored.Code, ignored.Body.String())
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=orders&endpoint="+endpoint.ID+"&event_id=paused-event")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "automation_paused") || strings.Contains(page.Body.String(), ">Inspect webhook run</a>") {
		t.Fatal("paused automation appeared to start a run")
	}
}

func TestAutomationConsoleWebhookReplacementRequiresReviewedVersion(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e, app, cookie := consoleFixture(t)
	endpoint := consoleWebhookEndpoint(t, e, app.Slug)
	apiBase := "/v1/apps/" + app.Slug + "/automations/other-target"
	spec := api.WorkflowSpec{Name: "other-target", Steps: []api.WorkflowStepSpec{{Name: "work", Path: "/other"}}}
	if response := e.Do(t, http.MethodPut, apiBase, api.SaveAutomationDraftRequest{Definition: spec}, nil); response.Code != http.StatusOK {
		t.Fatalf("save other target: %d %s", response.Code, response.Body.String())
	}
	if response := e.Do(t, http.MethodPost, apiBase+"/publish", api.PublishAutomationRequest{ExpectedVersion: 1}, nil); response.Code != http.StatusOK {
		t.Fatalf("publish other target: %d %s", response.Code, response.Body.String())
	}
	zero := int64(0)
	bound := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/inbound-webhooks/"+endpoint.ID+"/automation-binding",
		api.PutWebhookAutomationBindingRequest{ExpectedVersion: &zero, WorkflowName: "other-target", EventType: "order.*", TakeOverDelivery: true}, nil)
	var previous api.WebhookAutomationBindingResponse
	if bound.Code != http.StatusOK || json.Unmarshal(bound.Body.Bytes(), &previous) != nil {
		t.Fatalf("other binding: %d %s", bound.Code, bound.Body.String())
	}
	base := "/dashboard/apps/" + app.Slug + "/automations"
	page := apidserver.RestartProgressDashboardGET(e, cookie, base+"?template=webhook-enrichment")
	form := consoleWebhookForm(t, "orders")
	page = consolePost(e, cookie, page, base+"/save", form)
	saved := consoleVersion(t, e, app.Slug, "orders")
	definition, _ := json.Marshal(saved.Draft)
	form.Set("definition", string(definition))
	form.Set("expected_version", strconv.FormatInt(saved.Version, 10))
	page = consolePost(e, cookie, page, base+"/publish", form)
	if page.Code != http.StatusOK {
		t.Fatalf("publish replacement target: %d %s", page.Code, page.Body.String())
	}
	page = apidserver.RestartProgressDashboardGET(e, cookie, base+"?name=orders&endpoint="+endpoint.ID)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "other-target") {
		t.Fatalf("existing target was not shown before takeover: %d %s", page.Code, page.Body.String())
	}
	bind := url.Values{"name": {"orders"}, "endpoint_id": {endpoint.ID}, "binding_version": {strconv.FormatInt(previous.Version, 10)},
		"action_key": {uuid.NewString()}, "binding_event_type": {"order.created"}, "binding_filter": {"{}"}}
	if response := consolePost(e, cookie, page, base+"/bind-webhook", bind); response.Code != http.StatusBadRequest {
		t.Fatal("replacing another automation did not require consent")
	}
	bind.Set("confirm_bind-webhook", "yes")
	if response := consolePost(e, cookie, page, base+"/bind-webhook", bind); response.Code != http.StatusSeeOther {
		t.Fatalf("confirmed takeover: %d %s", response.Code, response.Body.String())
	}
	current, err := (*e.Store()).GetWebhookAutomationBinding(t.Context(), endpoint.ID)
	if err != nil || current.WorkflowName != "orders" || current.Version <= previous.Version {
		t.Fatal("confirmed binding was not versioned")
	}
	remove := url.Values{"name": {"orders"}, "endpoint_id": {endpoint.ID}, "binding_version": {strconv.FormatInt(previous.Version, 10)}, "confirm_unbind-webhook": {"yes"}}
	if response := consolePost(e, cookie, page, base+"/unbind-webhook", remove); response.Code != http.StatusConflict {
		t.Fatal("stale removal deleted a newer binding")
	}
	remove.Set("name", "other-target")
	remove.Set("binding_version", strconv.FormatInt(current.Version, 10))
	if response := consolePost(e, cookie, page, base+"/unbind-webhook", remove); response.Code != http.StatusBadRequest {
		t.Fatal("another automation's page removed the selected binding")
	}
}
