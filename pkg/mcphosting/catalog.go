package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// RPCError reports a JSON-RPC error without exposing server-controlled text.
type RPCError struct {
	Code int
}

func (e *RPCError) Error() string {
	if e.Code == RPCMethodNotFound {
		return fmt.Sprintf("MCP JSON-RPC error %d (method not found: the server does not implement this method)", e.Code)
	}
	return fmt.Sprintf("MCP JSON-RPC error %d", e.Code)
}

// RPCMethodNotFound is the JSON-RPC 2.0 code for an unsupported method.
const RPCMethodNotFound = -32601

// IsMethodNotFound reports whether err is the server declining a method.
func IsMethodNotFound(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == RPCMethodNotFound
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities is populated by server/discover on the current protocol
// and by initialize when explicitly checking legacy compatibility.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

func (c ServerCapabilities) HasCatalog() bool {
	return c.Tools != nil || c.Resources != nil || c.Prompts != nil
}

type Icon struct {
	Src      string   `json:"src"`
	MimeType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

// Resource is definition metadata only. Resource contents can contain private
// application data and are never part of a contract snapshot.
type Resource struct {
	URI         string          `json:"uri"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	MimeType    string          `json:"mimeType,omitempty"`
	Size        *int64          `json:"size,omitempty"`
	Icons       []Icon          `json:"icons,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string          `json:"uriTemplate"`
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	MimeType    string          `json:"mimeType,omitempty"`
	Icons       []Icon          `json:"icons,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Icons       []Icon           `json:"icons,omitempty"`
	Annotations json.RawMessage  `json:"annotations,omitempty"`
}

type Catalog struct {
	Capabilities      []string           `json:"capabilities,omitempty"`
	Tools             []Tool             `json:"tools"`
	Resources         []Resource         `json:"resources"`
	ResourceTemplates []ResourceTemplate `json:"resource_templates"`
	Prompts           []Prompt           `json:"prompts"`
}

// Discover requests the current protocol's capability advertisement. It does
// not list definitions or execute tools.
func (c *Client) Discover(ctx context.Context) (ServerCapabilities, Exchange, error) {
	if c.Version != ProtocolVersion {
		return ServerCapabilities{}, Exchange{}, fmt.Errorf("server/discover requires protocol %s", ProtocolVersion)
	}
	x, err := c.request(ctx, "server/discover", nil, nil, false)
	if err != nil {
		return ServerCapabilities{}, x, err
	}
	if x.SessionID != "" {
		return ServerCapabilities{}, x, fmt.Errorf("stateful MCP session detected")
	}
	var result struct {
		SupportedVersions []string           `json:"supportedVersions"`
		Capabilities      ServerCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return ServerCapabilities{}, x, fmt.Errorf("decode server/discover: %w", err)
	}
	versionSupported := false
	for _, version := range result.SupportedVersions {
		if version == c.Version {
			versionSupported = true
			break
		}
	}
	if !versionSupported {
		return ServerCapabilities{}, x, fmt.Errorf("server does not advertise protocol %s", c.Version)
	}
	if !result.Capabilities.HasCatalog() {
		return ServerCapabilities{}, x, fmt.Errorf("server/discover must advertise tools, resources or prompts")
	}
	c.Capabilities = result.Capabilities
	return result.Capabilities, x, nil
}

// DiscoverCatalog lists every catalog advertised by the server. It never reads
// resources, renders prompts, or invokes tools.
func (c *Client) DiscoverCatalog(ctx context.Context) (Catalog, Exchange, error) {
	capabilities := c.Capabilities
	var first Exchange
	var err error
	if c.Version == ProtocolVersion {
		capabilities, first, err = c.Discover(ctx)
		if err != nil {
			return Catalog{}, first, err
		}
	} else if !capabilities.HasCatalog() {
		return Catalog{}, first, fmt.Errorf("legacy initialize did not advertise tools, resources or prompts")
	}
	catalog := Catalog{
		Capabilities: capabilityNames(capabilities),
		Tools:        make([]Tool, 0), Resources: make([]Resource, 0),
		ResourceTemplates: make([]ResourceTemplate, 0), Prompts: make([]Prompt, 0),
	}
	if capabilities.Tools != nil {
		var x Exchange
		catalog.Tools, x, err = c.Tools(ctx)
		first.RejectedTools = x.RejectedTools
		if first.Result == nil {
			first = x
		}
		if err != nil {
			return Catalog{}, first, err
		}
	}
	if capabilities.Resources != nil {
		catalog.Resources, err = c.Resources(ctx)
		if err != nil {
			return Catalog{}, first, err
		}
		catalog.ResourceTemplates, err = c.ResourceTemplates(ctx)
		if err != nil {
			return Catalog{}, first, err
		}
	}
	if capabilities.Prompts != nil {
		catalog.Prompts, _, err = c.Prompts(ctx)
		if err != nil {
			return Catalog{}, first, err
		}
	}
	return catalog, first, nil
}

func capabilityNames(capabilities ServerCapabilities) []string {
	names := make([]string, 0, 3)
	if capabilities.Tools != nil {
		names = append(names, "tools")
	}
	if capabilities.Resources != nil {
		names = append(names, "resources")
	}
	if capabilities.Prompts != nil {
		names = append(names, "prompts")
	}
	return names
}

func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	items, err := listMetadata[Resource](ctx, c, "resources/list", "resources", func(v Resource) string { return v.URI })
	if err != nil {
		return nil, err
	}
	if _, err := NewCatalogContract(c.Version, Catalog{Resources: items}); err != nil {
		return nil, fmt.Errorf("invalid resources/list metadata: %w", err)
	}
	return items, nil
}

func (c *Client) ResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	items, err := listMetadata[ResourceTemplate](ctx, c, "resources/templates/list", "resourceTemplates", func(v ResourceTemplate) string { return v.URITemplate })
	if err != nil {
		return nil, err
	}
	if _, err := NewCatalogContract(c.Version, Catalog{ResourceTemplates: items}); err != nil {
		return nil, fmt.Errorf("invalid resources/templates/list metadata: %w", err)
	}
	return items, nil
}

func (c *Client) Prompts(ctx context.Context) ([]Prompt, Exchange, error) {
	items, x, err := listMetadataWithExchange[Prompt](ctx, c, "prompts/list", "prompts", func(v Prompt) string { return v.Name })
	if err == nil {
		_, err = NewCatalogContract(c.Version, Catalog{Prompts: items})
		if err != nil {
			err = fmt.Errorf("invalid prompts/list metadata: %w", err)
		}
	}
	return items, x, err
}

func (c *Client) ReadResource(ctx context.Context, uri string) (Exchange, error) {
	if uri == "" || len(uri) > 16<<10 {
		return Exchange{}, fmt.Errorf("resource URI must be nonempty and at most 16 KiB")
	}
	x, err := c.request(ctx, "resources/read", map[string]any{"uri": uri}, nil, false)
	if err != nil {
		return x, err
	}
	if x.SessionID != "" {
		return x, fmt.Errorf("stateful MCP session detected")
	}
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return x, fmt.Errorf("decode resource result: %w", err)
	}
	if result.ResultType != "" && result.ResultType != "complete" {
		x.Result = nil
		return x, fmt.Errorf("resource read requires %s handling; use an MCP SDK client for Tasks and input requests", result.ResultType)
	}
	return x, nil
}

func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]string) (Exchange, error) {
	if name == "" || len(name) > 1024 {
		return Exchange{}, fmt.Errorf("prompt name must be nonempty and at most 1 KiB")
	}
	if arguments == nil {
		arguments = map[string]string{}
	}
	x, err := c.request(ctx, "prompts/get", map[string]any{"name": name, "arguments": arguments}, nil, false)
	if err != nil {
		return x, err
	}
	if x.SessionID != "" {
		return x, fmt.Errorf("stateful MCP session detected")
	}
	var result struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(x.Result, &result); err != nil {
		return x, fmt.Errorf("decode prompt result: %w", err)
	}
	if result.ResultType != "" && result.ResultType != "complete" {
		x.Result = nil
		return x, fmt.Errorf("prompt requires %s handling; use an MCP SDK client for Tasks and input requests", result.ResultType)
	}
	return x, nil
}

func (c *Client) Resource(ctx context.Context, uri string) (Exchange, error) {
	return c.ReadResource(ctx, uri)
}

func listMetadata[T any](ctx context.Context, c *Client, method, field string, identity func(T) string) ([]T, error) {
	items, _, err := listMetadataWithExchange(ctx, c, method, field, identity)
	return items, err
}

func listMetadataWithExchange[T any](ctx context.Context, c *Client, method, field string, identity func(T) string) ([]T, Exchange, error) {
	items := make([]T, 0)
	seen := make(map[string]bool)
	params := make(map[string]any)
	totalBytes := 0
	var first Exchange
	for page := 0; page < 100; page++ {
		x, err := c.request(ctx, method, params, nil, false)
		if page == 0 {
			first = x
		}
		if err != nil {
			return nil, first, err
		}
		if x.SessionID != "" {
			return nil, first, fmt.Errorf("stateful MCP session detected")
		}
		totalBytes += len(x.Result)
		if totalBytes > maxResponseBytes {
			return nil, first, fmt.Errorf("%s inventory exceeds diagnostic memory limit", method)
		}
		var pageResult map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(x.Result))
		decoder.UseNumber()
		if err := decoder.Decode(&pageResult); err != nil {
			return nil, first, fmt.Errorf("decode %s: %w", method, err)
		}
		var resultType string
		if raw, ok := pageResult["resultType"]; ok {
			if err := json.Unmarshal(raw, &resultType); err != nil {
				return nil, first, fmt.Errorf("decode %s result type: %w", method, err)
			}
		}
		if resultType != "" && resultType != "complete" {
			return nil, first, fmt.Errorf("%s requires %s handling; use an MCP SDK client for Tasks and input requests", method, resultType)
		}
		var resultItems *[]T
		if raw, ok := pageResult[field]; ok {
			var decoded []T
			if err := decodeStrictMetadata(raw, &decoded); err != nil {
				return nil, first, fmt.Errorf("decode %s %s: %w", method, field, err)
			}
			resultItems = &decoded
		}
		if resultItems == nil {
			return nil, first, fmt.Errorf("%s result must include %s", method, field)
		}
		for _, item := range *resultItems {
			key := identity(item)
			if key == "" || seen["item:"+key] {
				return nil, first, fmt.Errorf("%s returned an empty or duplicate catalog identifier", method)
			}
			seen["item:"+key] = true
			items = append(items, item)
		}
		var next string
		if raw, ok := pageResult["nextCursor"]; ok {
			if err := json.Unmarshal(raw, &next); err != nil {
				return nil, first, fmt.Errorf("decode %s cursor: %w", method, err)
			}
		}
		if next == "" {
			return items, first, nil
		}
		if seen["cursor:"+next] {
			return nil, first, fmt.Errorf("%s repeated pagination cursor", method)
		}
		seen["cursor:"+next] = true
		params["cursor"] = next
	}
	return nil, first, fmt.Errorf("%s exceeded diagnostic page limit", method)
}

func decodeStrictMetadata(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("metadata must contain one JSON value")
	} else if err != io.EOF {
		return err
	}
	return nil
}
