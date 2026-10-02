package mcphosting

import (
	"encoding/json"
	"strings"
	"testing"
)

const validExecutionEvent = `{"event_version":1,"event":"mcp_request","request_id":"b9ddc41d-60c6-4f22-8d16-f6c3248c0453","protocol":"2026-07-28","rpc_method":"tools/call","tool":"add","outcome":"validation_error","reason":"input_validation","http_status":200,"duration_ms":2}`

func TestParseExecutionEvent(t *testing.T) {
	for _, protocol := range []string{ProtocolVersion, LegacyProtocolVersion, "unknown"} {
		line := strings.Replace(validExecutionEvent, ProtocolVersion, protocol, 1)
		event, err := ParseExecutionEvent(line)
		if err != nil || event.Protocol != protocol || event.Outcome != "validation_error" {
			t.Fatalf("event=%+v err=%v", event, err)
		}
	}
	callback := strings.ReplaceAll(validExecutionEvent, `"mcp_request"`, `"mcp_tool_call"`)
	callback = strings.ReplaceAll(callback, `,"reason":"input_validation","http_status":200`, "")
	if _, err := ParseExecutionEvent(callback); err != nil {
		t.Fatal(err)
	}
	withSecrets := strings.TrimSuffix(validExecutionEvent, "}") + `,"token":"private-token","arguments":{"name":"private-input"},"customer":"private-identity"}`
	event, err := ParseExecutionEvent(withSecrets)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil || strings.Contains(string(encoded), "private-") {
		t.Fatalf("unsafe DTO %s err=%v", encoded, err)
	}
}

func TestParseExecutionEventRejectsUnsafeRows(t *testing.T) {
	for _, line := range []string{
		"null", "{}", validExecutionEvent + "{}", strings.Repeat(" ", 8<<10) + validExecutionEvent,
		strings.ReplaceAll(validExecutionEvent, `"event_version":1`, `"event_version":2`),
		strings.ReplaceAll(validExecutionEvent, `"mcp_request"`, `"mcp_listening"`),
		strings.ReplaceAll(validExecutionEvent, `"duration_ms":2`, `"duration_ms":null`),
		strings.ReplaceAll(validExecutionEvent, `"duration_ms":2`, `"duration_ms":-1`),
		strings.ReplaceAll(validExecutionEvent, `"duration_ms":2`, `"duration_ms":0.5`),
		strings.ReplaceAll(validExecutionEvent, `"http_status":200`, `"http_status":999`),
		strings.ReplaceAll(validExecutionEvent, `"http_status":200`, `"http_status":null`),
		strings.ReplaceAll(validExecutionEvent, `"reason":"input_validation"`, `"reason":"private-error-message"`),
		strings.ReplaceAll(validExecutionEvent, `"tools/call"`, `"private-method"`),
		strings.ReplaceAll(validExecutionEvent, `"validation_error"`, `"private-outcome"`),
		strings.ReplaceAll(validExecutionEvent, `"tool":"add"`, `"tool":"\u001b[31m"`),
		strings.ReplaceAll(validExecutionEvent, `"2026-07-28"`, `"private-protocol"`),
		strings.ReplaceAll(validExecutionEvent, `"b9ddc41d-60c6-4f22-8d16-f6c3248c0453"`, `"private-request-id"`),
		strings.ReplaceAll(validExecutionEvent, `"mcp_request"`, `"mcp_tool_call"`),
	} {
		if _, err := ParseExecutionEvent(line); err == nil {
			t.Errorf("accepted unsafe row %.300s", line)
		}
	}
}
