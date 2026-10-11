package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
)

const maxCompletionSuggestions = 100
const maxCompletionContextArguments = 64

// CompletionReference identifies one prompt or resource template argument.
// Exactly one of Name and URI is used, according to Type.
type CompletionReference struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

// CompletionSuggestions is the bounded set of values returned for a partial
// prompt argument or resource-template variable.
type CompletionSuggestions struct {
	Values  []string `json:"values"`
	Total   *int     `json:"total,omitempty"`
	HasMore *bool    `json:"hasMore,omitempty"`
}

// Complete asks an MCP server for bounded suggestions. Context contains other
// prompt arguments or URI-template variables already resolved by the client.
func (c *Client) Complete(ctx context.Context, reference CompletionReference, argument, value string, contextArguments map[string]string) (CompletionSuggestions, error) {
	switch reference.Type {
	case "ref/prompt":
		if reference.Name == "" || len(reference.Name) > 1024 || reference.URI != "" {
			return CompletionSuggestions{}, fmt.Errorf("prompt completion requires a prompt name of at most 1 KiB")
		}
	case "ref/resource":
		if reference.URI == "" || len(reference.URI) > 16<<10 || reference.Name != "" {
			return CompletionSuggestions{}, fmt.Errorf("resource completion requires a URI template of at most 16 KiB")
		}
	default:
		return CompletionSuggestions{}, fmt.Errorf("completion reference type must be ref/prompt or ref/resource")
	}
	if argument == "" || len(argument) > 1024 {
		return CompletionSuggestions{}, fmt.Errorf("completion argument name must be nonempty and at most 1 KiB")
	}
	if len(value) > 16<<10 {
		return CompletionSuggestions{}, fmt.Errorf("completion partial value exceeds 16 KiB")
	}
	if len(contextArguments) > maxCompletionContextArguments {
		return CompletionSuggestions{}, fmt.Errorf("completion context exceeds %d arguments", maxCompletionContextArguments)
	}
	for key, item := range contextArguments {
		if key == "" || len(key) > 1024 || len(item) > 16<<10 {
			return CompletionSuggestions{}, fmt.Errorf("completion context contains an invalid argument")
		}
	}

	params := map[string]any{
		"ref":      reference,
		"argument": map[string]string{"name": argument, "value": value},
	}
	if len(contextArguments) > 0 {
		params["context"] = map[string]any{"arguments": contextArguments}
	}
	x, err := c.request(ctx, "completion/complete", params, nil, false)
	if err != nil {
		return CompletionSuggestions{}, err
	}
	if x.SessionID != "" {
		return CompletionSuggestions{}, fmt.Errorf("stateful MCP session detected")
	}
	var result struct {
		ResultType string `json:"resultType"`
		Completion *struct {
			Values  *[]string `json:"values"`
			Total   *int      `json:"total"`
			HasMore *bool     `json:"hasMore"`
		} `json:"completion"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return CompletionSuggestions{}, fmt.Errorf("decode completion result: %w", err)
	}
	if result.ResultType != "" && result.ResultType != "complete" {
		return CompletionSuggestions{}, fmt.Errorf("completion returned unsupported result type %q", result.ResultType)
	}
	if result.Completion == nil || result.Completion.Values == nil {
		return CompletionSuggestions{}, fmt.Errorf("completion result must include a values array")
	}
	values := *result.Completion.Values
	if len(values) > maxCompletionSuggestions {
		return CompletionSuggestions{}, fmt.Errorf("completion returned more than %d suggestions", maxCompletionSuggestions)
	}
	if total := result.Completion.Total; total != nil && (*total < 0 || *total < len(values)) {
		return CompletionSuggestions{}, fmt.Errorf("completion returned an invalid total")
	}
	return CompletionSuggestions{Values: values, Total: result.Completion.Total, HasMore: result.Completion.HasMore}, nil
}
