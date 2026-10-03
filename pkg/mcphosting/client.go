package mcphosting

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Diagnostic bounds are client memory protections, not product quotas.
const maxResponseBytes = 8 << 20

// Client is a sequential, diagnostic MCP client. It never retries tool calls
// or opens a standalone event stream. Use the official SDK for MRTR and Tasks.
type Client struct {
	Endpoint     string
	Version      string
	Token        string
	HTTP         *http.Client
	ExpectedAuth *AuthConfig
	nextID       int
}

type Exchange struct {
	Result          json.RawMessage     `json:"result,omitempty"`
	RequestID       string              `json:"request_id,omitempty"`
	ContentType     string              `json:"content_type"`
	WakeTier        string              `json:"wake_tier,omitempty"`
	DurationMS      int64               `json:"duration_ms"`
	ProgressMS      []int64             `json:"progress_ms,omitempty"`
	SessionID       string              `json:"-"`
	StreamingStatus api.StreamingStatus `json:"streaming_status,omitempty"`
	HTTPStatus      int                 `json:"-"`
	AuthChallenge   string              `json:"-"`
	RejectedTools   []RejectedTool      `json:"rejected_tools,omitempty"`
}

// RejectedTool makes incomplete discovery visible without hiding valid tools.
type RejectedTool struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  map[string]any  `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
}

func NewClient(endpoint, token, version string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("MCP URL must be an absolute HTTP(S) endpoint without credentials, query or fragment")
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return nil, fmt.Errorf("MCP URL requires HTTPS; HTTP is supported only on loopback")
	}
	if version == "" {
		version = ProtocolVersion
	}
	if version != ProtocolVersion && version != LegacyProtocolVersion {
		return nil, fmt.Errorf("unsupported diagnostic protocol version %q", version)
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("invalid bearer token")
	}
	return &Client{Endpoint: endpoint, Token: token, Version: version, HTTP: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) request(ctx context.Context, method string, params map[string]any, headers http.Header, notification bool) (Exchange, error) {
	p := make(map[string]any, len(params)+1)
	for k, v := range params {
		p[k] = v
	}
	if c.Version == ProtocolVersion {
		meta := map[string]any{"io.modelcontextprotocol/protocolVersion": c.Version, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "gregale-doctor", "version": "1.0.0"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
		if provided, ok := p["_meta"].(map[string]any); ok {
			for k, v := range provided {
				meta[k] = v
			}
		}
		p["_meta"] = meta
	}
	body := map[string]any{"jsonrpc": "2.0", "method": method, "params": p}
	c.nextID++
	id := c.nextID
	if !notification {
		body["id"] = id
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Exchange{}, fmt.Errorf("encode MCP request: %w", err)
	}
	if len(encoded) > 1<<20 {
		return Exchange{}, fmt.Errorf("MCP diagnostic request exceeds 1 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Exchange{}, fmt.Errorf("create MCP request: %w", err)
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", c.Version)
	if c.Version == ProtocolVersion {
		req.Header.Set("Mcp-Method", method)
		if name, ok := p["name"].(string); ok {
			req.Header.Set("Mcp-Name", encodeHeader(name))
		}
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	start := time.Now()
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Exchange{}, fmt.Errorf("send MCP request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	x := Exchange{WakeTier: res.Header.Get(wire.WakeHeader), SessionID: res.Header.Get("Mcp-Session-Id"), StreamingStatus: api.StreamingStatus(res.Header.Get(api.StreamingStatusHeader)), HTTPStatus: res.StatusCode, AuthChallenge: res.Header.Get("WWW-Authenticate")}
	if id := res.Header.Get("X-MCP-Request-ID"); ValidEventRequestID(id) {
		x.RequestID = id
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return x, httpResponseError(res)
	}
	if notification {
		if res.StatusCode != http.StatusAccepted {
			return x, fmt.Errorf("MCP notification returned HTTP %d, want 202", res.StatusCode)
		}
		return x, nil
	}
	x.ContentType, _, err = mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil {
		return x, fmt.Errorf("invalid MCP content type: %w", err)
	}
	reader := io.LimitReader(res.Body, maxResponseBytes+1)
	switch x.ContentType {
	case "application/json":
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			return x, fmt.Errorf("read MCP response: %w", readErr)
		}
		if len(data) > maxResponseBytes {
			return x, fmt.Errorf("MCP response exceeds diagnostic limit")
		}
		_, err = consumeMessage(data, id, &x, start)
	case "text/event-stream":
		err = consumeSSE(reader, id, &x, start)
	default:
		return x, fmt.Errorf("MCP response must be JSON or SSE, got %q", x.ContentType)
	}
	x.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		return x, err
	}
	if x.Result == nil {
		return x, fmt.Errorf("MCP response has no matching result")
	}
	return x, nil
}

func consumeMessage(data []byte, id int, x *Exchange, start time.Time) (bool, error) {
	var m struct {
		JSONRPC string `json:"jsonrpc"`
		ID      *int   `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			ProgressToken string `json:"progressToken"`
		} `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("decode MCP message: %w", err)
	}
	if m.JSONRPC != "2.0" {
		return false, fmt.Errorf("invalid MCP JSON-RPC version")
	}
	if m.ID == nil {
		if m.Method == "notifications/progress" && m.Params.ProgressToken == "gregale-doctor" {
			x.ProgressMS = append(x.ProgressMS, time.Since(start).Milliseconds())
		}
		return false, nil
	}
	if *m.ID != id {
		return false, fmt.Errorf("MCP response ID does not match request")
	}
	if m.Error != nil {
		return true, fmt.Errorf("MCP JSON-RPC error %d", m.Error.Code)
	}
	if m.Result == nil || bytes.Equal(m.Result, []byte("null")) {
		return true, fmt.Errorf("MCP response has no result")
	}
	x.Result = m.Result
	return true, nil
}

func consumeSSE(r io.Reader, id int, x *Exchange, start time.Time) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxResponseBytes {
			return fmt.Errorf("MCP SSE response exceeds diagnostic limit")
		}
		if line == "" && len(data) > 0 {
			done, err := consumeMessage([]byte(strings.Join(data, "\n")), id, x, start)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			data = nil
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP SSE: %w", err)
	}
	return fmt.Errorf("MCP SSE ended before its final result")
}

func (c *Client) Initialize(ctx context.Context) error {
	if c.Version != LegacyProtocolVersion {
		return nil
	}
	x, err := c.request(ctx, "initialize", map[string]any{"protocolVersion": c.Version, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "gregale-doctor", "version": "1.0.0"}}, nil, false)
	if err != nil {
		return err
	}
	if x.SessionID != "" {
		return fmt.Errorf("stateful MCP session detected; use a stateless server for this hosting profile")
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return fmt.Errorf("decode initialize: %w", err)
	}
	if result.ProtocolVersion != c.Version {
		return fmt.Errorf("server negotiated unsupported protocol %q", result.ProtocolVersion)
	}
	_, err = c.request(ctx, "notifications/initialized", nil, nil, true)
	return err
}

func (c *Client) Tools(ctx context.Context) ([]Tool, Exchange, error) {
	tools := make([]Tool, 0)
	seen := make(map[string]bool)
	params := make(map[string]any)
	totalBytes := 0
	var first Exchange
	for page := 0; page < 100; page++ {
		x, err := c.request(ctx, "tools/list", params, nil, false)
		if page == 0 {
			first = x
		}
		if err != nil {
			return nil, first, err
		}
		totalBytes += len(x.Result)
		if totalBytes > maxResponseBytes {
			return nil, first, fmt.Errorf("tool inventory exceeds diagnostic memory limit")
		}
		if x.SessionID != "" {
			return nil, first, fmt.Errorf("stateful MCP session detected")
		}
		var result struct {
			Tools      *[]Tool `json:"tools"`
			NextCursor string  `json:"nextCursor"`
		}
		decoder := json.NewDecoder(bytes.NewReader(x.Result))
		decoder.UseNumber() // Preserve exact numbers in discovered schema constraints.
		if err := decoder.Decode(&result); err != nil {
			return nil, first, fmt.Errorf("decode tools/list: %w", err)
		}
		if result.Tools == nil {
			return nil, first, fmt.Errorf("tools/list result must include tools")
		}
		for _, tool := range *result.Tools {
			if tool.Name == "" || seen["tool:"+tool.Name] {
				return nil, first, fmt.Errorf("invalid or duplicate MCP tool name")
			}
			if _, err := parameterHeaders(tool.InputSchema, nil); err != nil {
				first.RejectedTools = append(first.RejectedTools, RejectedTool{Name: tool.Name, Reason: err.Error()})
				seen["tool:"+tool.Name] = true
				continue
			}
			seen["tool:"+tool.Name] = true
			tools = append(tools, tool)
		}
		if result.NextCursor == "" {
			return tools, first, nil
		}
		if seen["cursor:"+result.NextCursor] {
			return nil, first, fmt.Errorf("tools/list repeated pagination cursor")
		}
		seen["cursor:"+result.NextCursor] = true
		params["cursor"] = result.NextCursor
	}
	return nil, first, fmt.Errorf("tools/list exceeded diagnostic page limit")
}

func (c *Client) Call(ctx context.Context, tool Tool, args map[string]any, progress bool) (Exchange, error) {
	headers, err := parameterHeaders(tool.InputSchema, args)
	if err != nil {
		return Exchange{}, err
	}
	params := map[string]any{"name": tool.Name, "arguments": args}
	if progress {
		params["_meta"] = map[string]any{"progressToken": "gregale-doctor"}
	}
	x, err := c.request(ctx, "tools/call", params, headers, false)
	if err != nil {
		return x, err
	}
	if x.SessionID != "" {
		return x, fmt.Errorf("stateful MCP session detected")
	}
	var result struct {
		IsError    bool               `json:"isError"`
		ResultType string             `json:"resultType"`
		Content    *[]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return x, fmt.Errorf("decode tool result: %w", err)
	}
	if result.IsError {
		return x, fmt.Errorf("MCP tool returned isError=true")
	}
	if result.ResultType != "" && result.ResultType != "complete" {
		return x, fmt.Errorf("tool requires %s handling; use an MCP SDK client for Tasks and input requests", result.ResultType)
	}
	if result.Content == nil {
		return x, fmt.Errorf("MCP tool result must contain a content array")
	}
	return x, nil
}

func (c *Client) RejectsUntrustedOrigin(ctx context.Context) error {
	_, err := c.request(ctx, "tools/list", nil, http.Header{"Origin": []string{"https://gregale-mcp-origin-check.invalid"}}, false)
	var upstream *api.APIError
	if !errors.As(err, &upstream) || upstream.Problem.Status != http.StatusForbidden {
		return fmt.Errorf("untrusted Origin must return HTTP 403")
	}
	return nil
}

func encodeHeader(value string) string {
	sentinel := strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=")
	safe := strings.TrimSpace(value) == value && !sentinel
	for _, r := range value {
		if (r < 32 && r != '\t') || r > 126 {
			safe = false
		}
	}
	if safe {
		return value
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}
