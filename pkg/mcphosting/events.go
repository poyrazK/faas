package mcphosting

import (
	"encoding/json"
	"errors"
	"regexp"
)

// ExecutionEvent is the starter's versioned, redacted application diagnostic.
// It is not a platform-authenticated audit record. Unknown fields are discarded.
type ExecutionEvent struct {
	Version    int    `json:"event_version"`
	Event      string `json:"event"`
	RequestID  string `json:"request_id"`
	Protocol   string `json:"protocol"`
	Method     string `json:"rpc_method"`
	Tool       string `json:"tool,omitempty"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	HTTPStatus *int   `json:"http_status,omitempty"`
	DurationMS *int64 `json:"duration_ms"`
}

var eventRequestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var eventToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func ValidEventRequestID(id string) bool  { return eventRequestID.MatchString(id) }
func ValidEventToolName(name string) bool { return eventToolName.MatchString(name) }

func ValidEventOutcome(outcome string) bool {
	switch outcome {
	case "success", "denied", "validation_error", "tool_error", "error", "cancelled", "protocol_error":
		return true
	}
	return false
}

// ParseExecutionEvent bounds diagnostic memory and rejects unversioned or invalid
// rows. Re-encoding this DTO never forwards arbitrary runtime log fields.
func ParseExecutionEvent(line string) (ExecutionEvent, error) {
	var event ExecutionEvent
	if len(line) > 8<<10 || json.Unmarshal([]byte(line), &event) != nil || event.Version != 1 {
		return ExecutionEvent{}, errors.New("invalid MCP event format")
	}
	if !ValidEventRequestID(event.RequestID) || !ValidEventOutcome(event.Outcome) ||
		(event.Tool != "" && !ValidEventToolName(event.Tool)) || event.DurationMS == nil || *event.DurationMS < 0 {
		return ExecutionEvent{}, errors.New("invalid MCP event fields")
	}
	switch event.Protocol {
	case ProtocolVersion, LegacyProtocolVersion, "unknown":
	default:
		return ExecutionEvent{}, errors.New("invalid MCP event protocol")
	}
	switch event.Method {
	case "initialize", "notifications/initialized", "notifications/cancelled", "ping", "tools/list", "tools/call", "unknown":
	default:
		return ExecutionEvent{}, errors.New("invalid MCP event method")
	}
	switch event.Reason {
	case "", "origin_not_allowed", "authentication_required", "invalid_token", "insufficient_scope", "tool_access_denied",
		"input_validation", "output_validation", "invalid_body", "internal_error", "transport_error", "unsupported_call":
	default:
		return ExecutionEvent{}, errors.New("invalid MCP event reason")
	}
	switch event.Event {
	case "mcp_request":
		if event.HTTPStatus == nil || (*event.HTTPStatus != 0 && (*event.HTTPStatus < 100 || *event.HTTPStatus > 599)) {
			return ExecutionEvent{}, errors.New("invalid MCP request status")
		}
	case "mcp_tool_call":
		if event.Tool == "" || event.HTTPStatus != nil || event.Reason != "" {
			return ExecutionEvent{}, errors.New("invalid MCP callback event")
		}
	default:
		return ExecutionEvent{}, errors.New("unknown MCP event kind")
	}
	return event, nil
}
