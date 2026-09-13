package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a small management-plane client for realtimed. It is intended
// for apid, workers, and callback handlers that need to send/close a live
// connection or publish to a channel; it never owns WebSocket sockets.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// ManagementError preserves an HTTP status returned by realtimed's local
// management socket. Callers that expose a public API can map a missing live
// connection (410) separately from an unavailable owner (503) without
// parsing error strings.
type ManagementError struct {
	StatusCode int
}

func (e *ManagementError) Error() string {
	if e == nil {
		return "realtime: management request failed"
	}
	return fmt.Sprintf("realtime: management request returned HTTP %d", e.StatusCode)
}

// NewUnixClient returns a client connected to a local realtimed Unix socket.
// The socket's DAC permissions remain the authorization boundary.
func NewUnixClient(socket string) *Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: false,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{
		BaseURL:    "http://realtimed",
		HTTPClient: &http.Client{Transport: transport},
	}
}

// RegisterEndpoint makes an endpoint available on this realtimed node.
func (c *Client) RegisterEndpoint(ctx context.Context, endpoint Endpoint) error {
	request := endpointRequest{
		ID:                endpoint.ID,
		AppID:             endpoint.AppID,
		AccountID:         endpoint.AccountID,
		CallbackURL:       endpoint.CallbackURL,
		ConnectPath:       endpoint.ConnectPath,
		MessagePath:       endpoint.MessagePath,
		DisconnectPath:    endpoint.DisconnectPath,
		CallbackAuthToken: endpoint.CallbackAuthToken,
		AuthToken:         endpoint.AuthToken,
	}
	return c.do(ctx, http.MethodPost, "/internal/endpoints", request, nil)
}

// RemoveEndpoint stops new connections for an endpoint.
func (c *Client) RemoveEndpoint(ctx context.Context, endpointID string) error {
	return c.do(ctx, http.MethodDelete, "/internal/endpoints/"+pathPart(endpointID), nil, nil)
}

// Send queues a message to one live connection.
func (c *Client) Send(ctx context.Context, connectionID string, message Message) error {
	return c.do(ctx, http.MethodPost, "/internal/connections/"+pathPart(connectionID)+":send", messageRequest{
		DataBase64: encodeMessage(message),
		Binary:     message.Binary,
	}, nil)
}

// CloseConnection closes one live connection.
func (c *Client) CloseConnection(ctx context.Context, connectionID, reason string) error {
	return c.do(ctx, http.MethodPost, "/internal/connections/"+pathPart(connectionID)+":close", struct {
		Reason string `json:"reason,omitempty"`
	}{Reason: reason}, nil)
}

// Subscribe adds a connection to an endpoint-scoped channel.
func (c *Client) Subscribe(ctx context.Context, connectionID, channel string) error {
	return c.do(ctx, http.MethodPut, "/internal/connections/"+pathPart(connectionID)+"/subscriptions/"+pathPart(channel), nil, nil)
}

// Unsubscribe removes a connection from an endpoint-scoped channel.
func (c *Client) Unsubscribe(ctx context.Context, connectionID, channel string) error {
	return c.do(ctx, http.MethodDelete, "/internal/connections/"+pathPart(connectionID)+"/subscriptions/"+pathPart(channel), nil, nil)
}

// Publish queues a message to all subscribed local connections. The returned
// count is the number of connections that accepted the message into a queue.
func (c *Client) Publish(ctx context.Context, endpointID, channel string, message Message) (int, error) {
	var response struct {
		Queued int `json:"queued"`
	}
	err := c.do(ctx, http.MethodPost, "/internal/endpoints/"+pathPart(endpointID)+"/channels/"+pathPart(channel)+":publish", messageRequest{
		DataBase64: encodeMessage(message),
		Binary:     message.Binary,
	}, &response)
	return response.Queued, err
}

// Connections returns the local connection registry.
func (c *Client) Connections(ctx context.Context) ([]ConnectionInfo, error) {
	var response []ConnectionInfo
	err := c.do(ctx, http.MethodGet, "/internal/connections", nil, &response)
	return response, err
}

// Stats returns the local realtime counters.
func (c *Client) Stats(ctx context.Context) (Stats, error) {
	var response Stats
	err := c.do(ctx, http.MethodGet, "/internal/stats", nil, &response)
	return response, err
}

func encodeMessage(message Message) string {
	return base64.StdEncoding.EncodeToString(message.Data)
}

func pathPart(value string) string {
	return url.PathEscape(value)
}

func (c *Client) do(ctx context.Context, method, path string, payload any, result any) error {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("realtime: client is not configured")
	}
	var body *strings.Reader
	if payload == nil {
		body = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("realtime: encode management request: %w", err)
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("realtime: build management request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("realtime: management request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &ManagementError{StatusCode: resp.StatusCode}
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("realtime: decode management response: %w", err)
		}
	}
	return nil
}
