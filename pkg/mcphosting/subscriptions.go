package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// CatalogNotificationFilter selects catalog list changes and resource updates
// to receive over a subscriptions/listen stream.
type CatalogNotificationFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged,omitempty"`
	ResourcesListChanged  bool     `json:"resourcesListChanged,omitempty"`
	PromptsListChanged    bool     `json:"promptsListChanged,omitempty"`
	ResourceSubscriptions []string `json:"resourceSubscriptions,omitempty"`
}

// CatalogChange is the MCP notification method for a changed catalog or resource.
type CatalogChange string

const (
	CatalogToolsListChanged     CatalogChange = "notifications/tools/list_changed"
	CatalogResourcesListChanged CatalogChange = "notifications/resources/list_changed"
	CatalogPromptsListChanged   CatalogChange = "notifications/prompts/list_changed"
	CatalogResourceUpdated      CatalogChange = "notifications/resources/updated"
)

var (
	// ErrSubscriptionsUnsupported indicates the server does not support the
	// catalog change subscription method or one of its requested filters.
	ErrSubscriptionsUnsupported = errors.New("MCP server does not support catalog change subscriptions")
	// ErrSubscriptionClosed indicates the server completed a catalog stream;
	// callers may snapshot and open another listener.
	ErrSubscriptionClosed = errors.New("MCP catalog subscription stream closed")
	// ErrSubscriptionInterrupted indicates a catalog stream ended unexpectedly.
	ErrSubscriptionInterrupted = errors.New("MCP catalog subscription stream interrupted")
)

// ListenCatalogChange waits for one selected catalog notification or subscribed
// resource update. The stream is closed when the first change arrives;
// reconnecting opens a new listener without retaining transport state.
func (c *Client) ListenCatalogChange(ctx context.Context, filter CatalogNotificationFilter) (CatalogChange, error) {
	if c.Version != ProtocolVersion {
		return "", ErrSubscriptionsUnsupported
	}
	requested := map[string]bool{
		"toolsListChanged":     filter.ToolsListChanged,
		"resourcesListChanged": filter.ResourcesListChanged,
		"promptsListChanged":   filter.PromptsListChanged,
	}
	if !filter.ToolsListChanged && !filter.ResourcesListChanged && !filter.PromptsListChanged && len(filter.ResourceSubscriptions) == 0 {
		return "", errors.New("MCP catalog subscription requires at least one notification type")
	}
	if len(filter.ResourceSubscriptions) > 64 {
		return "", errors.New("MCP resource subscription cannot exceed 64 URIs")
	}
	requestedResources := make(map[string]bool, len(filter.ResourceSubscriptions))
	for _, uri := range filter.ResourceSubscriptions {
		if uri == "" || len(uri) > 16<<10 || requestedResources[uri] {
			return "", errors.New("MCP resource subscription contains an invalid or duplicate URI")
		}
		requestedResources[uri] = true
	}

	c.nextID++
	id := c.nextID
	params := map[string]any{
		"notifications": map[string]any{
			"toolsListChanged":      filter.ToolsListChanged,
			"resourcesListChanged":  filter.ResourcesListChanged,
			"promptsListChanged":    filter.PromptsListChanged,
			"resourceSubscriptions": filter.ResourceSubscriptions,
			"taskIds":               []string{},
		},
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": c.Version,
			"io.modelcontextprotocol/clientInfo":      map[string]string{"name": "gregale-cli", "version": "1.0.0"},
		},
	}
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "subscriptions/listen", "params": params})
	if err != nil {
		return "", fmt.Errorf("encode MCP catalog subscription: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("create MCP catalog subscription: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", c.Version)
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := *c.HTTP
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		switch res.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusUnsupportedMediaType, http.StatusNotImplemented:
			return "", ErrSubscriptionsUnsupported
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, httpResponseError(res))
		default:
			if res.StatusCode >= 500 {
				return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, httpResponseError(res))
			}
			return "", httpResponseError(res)
		}
	}
	if res.Header.Get("Mcp-Session-Id") != "" {
		return "", errors.New("stateful MCP session detected")
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil {
		return "", ErrSubscriptionsUnsupported
	}
	if contentType == "application/json" {
		body, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
		if readErr != nil {
			return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, readErr)
		}
		if len(body) > maxResponseBytes {
			return "", errors.New("MCP catalog subscription response exceeds diagnostic limit")
		}
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &message) != nil || message.JSONRPC != "2.0" {
			return "", errors.New("MCP catalog subscription returned invalid JSON")
		}
		var responseID int
		if json.Unmarshal(message.ID, &responseID) != nil || responseID != id {
			return "", errors.New("MCP catalog subscription response ID does not match request")
		}
		if message.Error != nil {
			if unsupportedTaskSubscriptionCode(message.Error.Code) {
				return "", ErrSubscriptionsUnsupported
			}
			if message.Error.Code == -32603 {
				return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, &RPCError{Code: message.Error.Code})
			}
			return "", &RPCError{Code: message.Error.Code}
		}
		return "", ErrSubscriptionsUnsupported
	}
	if contentType != "text/event-stream" {
		return "", ErrSubscriptionsUnsupported
	}

	acknowledged := false
	var change CatalogChange
	_, err = scanTaskSSE(res.Body, func(data []byte) (bool, error) {
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &message); err != nil || message.JSONRPC != "2.0" {
			return true, errors.New("MCP catalog subscription contains an invalid JSON-RPC message")
		}
		if len(message.ID) > 0 && !bytes.Equal(message.ID, []byte("null")) {
			var responseID int
			if err := json.Unmarshal(message.ID, &responseID); err != nil || responseID != id {
				return true, errors.New("MCP catalog subscription response ID does not match request")
			}
			if message.Error != nil {
				if unsupportedTaskSubscriptionCode(message.Error.Code) {
					return true, ErrSubscriptionsUnsupported
				}
				if message.Error.Code == -32603 {
					return true, fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, &RPCError{Code: message.Error.Code})
				}
				return true, &RPCError{Code: message.Error.Code}
			}
			if !acknowledged {
				return true, ErrSubscriptionsUnsupported
			}
			var result struct {
				ResultType string `json:"resultType"`
			}
			if json.Unmarshal(message.Result, &result) != nil || result.ResultType != "complete" {
				return true, errors.New("MCP catalog subscription ended with an invalid response")
			}
			return true, ErrSubscriptionClosed
		}

		if message.Method == "notifications/subscriptions/acknowledged" {
			var ack struct {
				Notifications map[string]json.RawMessage `json:"notifications"`
			}
			if err := json.Unmarshal(message.Params, &ack); err != nil || ack.Notifications == nil {
				return true, ErrSubscriptionsUnsupported
			}
			for name, wanted := range requested {
				if wanted {
					var honored bool
					if json.Unmarshal(ack.Notifications[name], &honored) != nil || !honored {
						return true, ErrSubscriptionsUnsupported
					}
				}
			}
			if len(filter.ResourceSubscriptions) > 0 {
				var honored []string
				if json.Unmarshal(ack.Notifications["resourceSubscriptions"], &honored) != nil {
					return true, ErrSubscriptionsUnsupported
				}
				honoredResources := make(map[string]bool, len(honored))
				for _, uri := range honored {
					honoredResources[uri] = true
				}
				for uri := range requestedResources {
					if !honoredResources[uri] {
						return true, ErrSubscriptionsUnsupported
					}
				}
			}
			acknowledged = true
			return false, nil
		}
		if !acknowledged {
			return true, errors.New("MCP catalog notification arrived before subscription acknowledgement")
		}
		switch message.Method {
		case string(CatalogToolsListChanged):
			if requested["toolsListChanged"] {
				change = CatalogToolsListChanged
			}
		case string(CatalogResourcesListChanged):
			if requested["resourcesListChanged"] {
				change = CatalogResourcesListChanged
			}
		case string(CatalogPromptsListChanged):
			if requested["promptsListChanged"] {
				change = CatalogPromptsListChanged
			}
		case string(CatalogResourceUpdated):
			var params struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(message.Params, &params); err != nil || params.URI == "" {
				return true, errors.New("MCP resource update notification has an invalid URI")
			}
			if requestedResources[params.URI] {
				change = CatalogResourceUpdated
			}
		}
		return change != "", nil
	})
	if err != nil {
		if errors.Is(err, errTaskSubscriptionInterrupted) {
			return "", fmt.Errorf("%w: %w", ErrSubscriptionInterrupted, err)
		}
		return "", err
	}
	if change != "" {
		return change, nil
	}
	if !acknowledged {
		return "", ErrSubscriptionsUnsupported
	}
	return "", ErrSubscriptionClosed
}

// ListenResourceUpdated listens for one selected resource URI and returns it
// when the server publishes notifications/resources/updated.
func (c *Client) ListenResourceUpdated(ctx context.Context, uri string) (string, error) {
	if uri == "" || len(uri) > 16<<10 {
		return "", errors.New("MCP resource subscription URI must be nonempty and at most 16 KiB")
	}
	change, err := c.ListenCatalogChange(ctx, CatalogNotificationFilter{ResourceSubscriptions: []string{uri}})
	if err != nil {
		return "", err
	}
	if change != CatalogResourceUpdated {
		return "", errors.New("MCP resource subscription received an unexpected notification")
	}
	return uri, nil
}
