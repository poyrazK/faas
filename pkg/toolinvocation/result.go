package toolinvocation

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var credentials = regexp.MustCompile(`(?i)\b(?:fp_(?:live|test|oidc)_|gmcp_(?:at|rt|code|csrf|browser)_)[a-z0-9_-]+|\bBearer[ \t]+[a-z0-9._~+/-]+=*`)

// Result retains only complete MCP content and structuredContent, stripping
// transport metadata, private input state and known credential reflections.
func Result(raw []byte, secrets ...string) (api.ToolInvocationOutcome, error) {
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	var kind string
	if value := original["resultType"]; len(value) > 0 && json.Unmarshal(value, &kind) != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	if kind != "" && kind != "complete" {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	projected := map[string]json.RawMessage{}
	for _, key := range []string{"content", "structuredContent", "isError"} {
		if value, ok := original[key]; ok {
			projected[key] = value
		}
	}
	data, err := json.Marshal(projected)
	if err != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	var isError bool
	if value, ok := projected["isError"]; ok && json.Unmarshal(value, &isError) != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	if !validResult(data, isError) {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	redacted := false
	clean := func(value string) string {
		before := value
		for _, secret := range secrets {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[REDACTED]")
			}
		}
		value = credentials.ReplaceAllString(safetext.Clean(value), "[REDACTED]")
		redacted = redacted || before != value
		return value
	}
	cleanResult(value, clean)
	data, err = json.Marshal(value)
	if err != nil {
		return api.ToolInvocationOutcome{}, ErrInvalid
	}
	status := "completed"
	if isError {
		status = "tool_error"
	}
	out := api.ToolInvocationOutcome{Status: status, Code: "result_received", ResultSHA256: toolpublication.Digest(data), ResultBytes: len(data), Redacted: redacted}
	if len(data) > api.ToolInvocationResultMaxBytes {
		out.ResultOmitted = true
	} else {
		out.Result = data
	}
	return out, nil
}

func cleanResult(value any, clean func(string) string) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if text, ok := item.(string); ok {
				item = clean(text)
			} else {
				cleanResult(item, clean)
			}
			safe := clean(key)
			if safe != key {
				delete(v, key)
			}
			v[safe] = item
		}
	case []any:
		for index, item := range v {
			if text, ok := item.(string); ok {
				v[index] = clean(text)
			} else {
				cleanResult(item, clean)
			}
		}
	}
}
