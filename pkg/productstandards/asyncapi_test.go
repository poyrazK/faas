package productstandards

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAsyncAPIContract(t *testing.T) {
	path := filepath.Join("..", "..", "api", "asyncapi.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if got := document["asyncapi"]; got != "3.0.0" {
		t.Fatalf("asyncapi version = %v, want 3.0.0", got)
	}
	if got := document["defaultContentType"]; got != "application/cloudevents+json" {
		t.Fatalf("defaultContentType = %v, want CloudEvents structured JSON", got)
	}
	info := object(t, document, "info")
	if got := info["version"]; got != "1.1.0" {
		t.Fatalf("info.version = %v, want 1.1.0 after workflow channel expansion", got)
	}

	channels := object(t, document, "channels")
	operations := object(t, document, "operations")
	components := object(t, document, "components")
	messages := object(t, components, "messages")
	schemas := object(t, components, "schemas")
	securitySchemes := object(t, components, "securitySchemes")
	_ = object(t, securitySchemes, "bearerAuth")

	wantEvents := map[string]string{
		"appParked":               "app.parked",
		"appWoken":                "app.woken",
		"usageStatementFinalized": "usage_statement.finalized",
	}
	for channelName, eventName := range wantEvents {
		channel := object(t, channels, channelName)
		if channel["address"] != eventName {
			t.Errorf("channels.%s.address = %v, want %q", channelName, channel["address"], eventName)
		}
		channelMessages := object(t, channel, "messages")
		if len(channelMessages) != 1 {
			t.Errorf("channels.%s has %d messages, want 1", channelName, len(channelMessages))
		}
	}
	workflowChannel := object(t, channels, "workflowExternalEvent")
	if workflowChannel["address"] != "/v1/workflows/runs/{id}/events" {
		t.Errorf("channels.workflowExternalEvent.address = %v, want workflow event endpoint", workflowChannel["address"])
	}
	parameters := object(t, workflowChannel, "parameters")
	_ = object(t, parameters, "id")
	workflowMessages := object(t, workflowChannel, "messages")
	if len(workflowMessages) != 1 {
		t.Errorf("channels.workflowExternalEvent has %d messages, want 1", len(workflowMessages))
	}

	for operationName, channelName := range map[string]string{
		"deliverAppParked":               "appParked",
		"deliverAppWoken":                "appWoken",
		"deliverUsageStatementFinalized": "usageStatementFinalized",
	} {
		operation := object(t, operations, operationName)
		if operation["action"] != "send" {
			t.Errorf("operations.%s.action = %v, want send", operationName, operation["action"])
		}
		bindings := object(t, operation, "bindings")
		httpBinding := object(t, bindings, "http")
		if httpBinding["method"] != "POST" {
			t.Errorf("operations.%s HTTP method = %v, want POST", operationName, httpBinding["method"])
		}
		refs := operation["messages"].([]any)
		if len(refs) != 1 {
			t.Errorf("operations.%s has %d message refs, want 1", operationName, len(refs))
		}
		ref := refs[0].(map[string]any)["$ref"]
		wantRef := "#/channels/" + channelName + "/messages/"
		if ref == nil || len(ref.(string)) <= len(wantRef) || ref.(string)[:len(wantRef)] != wantRef {
			t.Errorf("operations.%s message ref = %v, want channel %s", operationName, ref, channelName)
		}
	}
	workflowOperation := object(t, operations, "receiveWorkflowExternalEvent")
	if workflowOperation["action"] != "receive" {
		t.Errorf("operations.receiveWorkflowExternalEvent.action = %v, want receive", workflowOperation["action"])
	}
	workflowBindings := object(t, workflowOperation, "bindings")
	workflowHTTPBinding := object(t, workflowBindings, "http")
	if workflowHTTPBinding["method"] != "POST" {
		t.Errorf("operations.receiveWorkflowExternalEvent HTTP method = %v, want POST", workflowHTTPBinding["method"])
	}
	security := workflowOperation["security"].([]any)
	if len(security) != 1 {
		t.Errorf("operations.receiveWorkflowExternalEvent security entries = %d, want 1", len(security))
	} else if _, ok := security[0].(map[string]any)["bearerAuth"]; !ok {
		t.Errorf("operations.receiveWorkflowExternalEvent security = %v, want bearerAuth", security)
	}
	workflowRefs := workflowOperation["messages"].([]any)
	if len(workflowRefs) != 1 || workflowRefs[0].(map[string]any)["$ref"] != "#/channels/workflowExternalEvent/messages/workflowExternalEvent" {
		t.Errorf("operations.receiveWorkflowExternalEvent message ref = %v, want workflow channel message", workflowOperation["messages"])
	}

	for _, messageName := range []string{"AppParked", "AppWoken", "UsageStatementFinalized"} {
		message := object(t, messages, messageName)
		if message["contentType"] != "application/cloudevents+json" {
			t.Errorf("components.messages.%s contentType = %v, want CloudEvents structured JSON", messageName, message["contentType"])
		}
		bindings := object(t, message, "bindings")
		httpBinding := object(t, bindings, "http")
		if httpBinding["bindingVersion"] != "0.3.0" {
			t.Errorf("components.messages.%s HTTP binding version = %v, want 0.3.0", messageName, httpBinding["bindingVersion"])
		}
	}
	workflowMessage := object(t, messages, "WorkflowExternalEvent")
	if workflowMessage["contentType"] != "application/json" {
		t.Errorf("components.messages.WorkflowExternalEvent contentType = %v, want application/json", workflowMessage["contentType"])
	}
	workflowMessageBindings := object(t, workflowMessage, "bindings")
	workflowMessageHTTPBinding := object(t, workflowMessageBindings, "http")
	if workflowMessageHTTPBinding["bindingVersion"] != "0.3.0" {
		t.Errorf("components.messages.WorkflowExternalEvent HTTP binding version = %v, want 0.3.0", workflowMessageHTTPBinding["bindingVersion"])
	}

	for _, schemaName := range []string{"CloudEventBase", "WebhookHeaders", "AppParkedData", "AppWokenData", "UsageStatementFinalizedData", "WorkflowEventHeaders", "WorkflowExternalEventPayload"} {
		_ = object(t, schemas, schemaName)
	}
}

func object(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key]
	if !ok {
		t.Fatalf("missing %q", key)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%q = %T, want object", key, value)
	}
	return object
}
