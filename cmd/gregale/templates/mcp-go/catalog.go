package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// MCPRequestContext contains verified caller details safe for application
// handlers. It never contains the bearer token.
type MCPRequestContext struct {
	Subject  string
	ClientID string
	Scopes   []string
}

// MCPToolHandler runs a registered tool. The handler must validate arguments
// against its advertised inputSchema and return a normal MCP tool result.
type MCPToolHandler func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error)

// MCPResourceHandler reads a registered resource and returns MCP resource
// contents. Variables contains URI-template values for template resources.
type MCPResourceHandler func(context.Context, string, map[string]string, *MCPRequestContext) ([]map[string]any, error)

// MCPPromptHandler builds a registered prompt result.
type MCPPromptHandler func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error)

type mcpCatalogEntry struct {
	definition map[string]any
	version    uint64
	tool       MCPToolHandler
	resource   MCPResourceHandler
	prompt     MCPPromptHandler
}

type mcpCatalog struct {
	mu          sync.RWMutex
	version     uint64
	tools       map[string]mcpCatalogEntry
	resources   map[string]mcpCatalogEntry
	prompts     map[string]mcpCatalogEntry
	subscribers map[chan struct{}]struct{}
}

var mcpRuntimeCatalog = newMCPCatalog()

func newMCPCatalog() *mcpCatalog {
	return &mcpCatalog{
		tools:       make(map[string]mcpCatalogEntry),
		resources:   make(map[string]mcpCatalogEntry),
		prompts:     make(map[string]mcpCatalogEntry),
		subscribers: make(map[chan struct{}]struct{}),
	}
}

func init() {
	for _, definition := range toolDefinitions {
		if err := mcpRuntimeCatalog.seed("tool", definition); err != nil {
			panic(err)
		}
	}
	for _, definition := range resourceDefinitions {
		if err := mcpRuntimeCatalog.seed("resource", definition); err != nil {
			panic(err)
		}
	}
	for _, definition := range resourceTemplateDefinitions {
		if err := mcpRuntimeCatalog.seed("resource", definition); err != nil {
			panic(err)
		}
	}
	for _, definition := range promptDefinitions {
		if err := mcpRuntimeCatalog.seed("prompt", definition); err != nil {
			panic(err)
		}
	}
}

// RegisterMCPTool adds a tool to this process. A configured OAuth tool_scopes
// policy must also contain its name before callers can see or call it.
func RegisterMCPTool(definition map[string]any, handler MCPToolHandler) error {
	if handler == nil {
		return errors.New("MCP tool handler is required")
	}
	return mcpRuntimeCatalog.register("tool", "", definition, handler, nil, nil, false, true)
}

// ReplaceMCPTool updates an existing tool without changing its position in the
// caller-visible catalog. It returns an error when name is not registered.
func ReplaceMCPTool(name string, definition map[string]any, handler MCPToolHandler) error {
	if handler == nil {
		return errors.New("MCP tool handler is required")
	}
	return mcpRuntimeCatalog.register("tool", name, definition, handler, nil, nil, true, true)
}

// RemoveMCPTool removes a tool by its catalog name.
func RemoveMCPTool(name string) bool { return mcpRuntimeCatalog.remove("tool", name) }

// RegisterMCPResource adds either a concrete resource (uri) or a resource
// template (uriTemplate). Handlers receive the requested URI and parsed values.
func RegisterMCPResource(definition map[string]any, handler MCPResourceHandler) error {
	if handler == nil {
		return errors.New("MCP resource handler is required")
	}
	return mcpRuntimeCatalog.register("resource", "", definition, nil, handler, nil, false, true)
}

// ReplaceMCPResource updates a resource by its catalog name.
func ReplaceMCPResource(name string, definition map[string]any, handler MCPResourceHandler) error {
	if handler == nil {
		return errors.New("MCP resource handler is required")
	}
	return mcpRuntimeCatalog.register("resource", name, definition, nil, handler, nil, true, true)
}

// RemoveMCPResource removes a concrete resource or template by catalog name.
func RemoveMCPResource(name string) bool { return mcpRuntimeCatalog.remove("resource", name) }

// RegisterMCPPrompt adds a prompt to this process. A configured OAuth
// prompt_scopes policy must also contain its name before callers can see it.
func RegisterMCPPrompt(definition map[string]any, handler MCPPromptHandler) error {
	if handler == nil {
		return errors.New("MCP prompt handler is required")
	}
	return mcpRuntimeCatalog.register("prompt", "", definition, nil, nil, handler, false, true)
}

// ReplaceMCPPrompt updates an existing prompt by its catalog name.
func ReplaceMCPPrompt(name string, definition map[string]any, handler MCPPromptHandler) error {
	if handler == nil {
		return errors.New("MCP prompt handler is required")
	}
	return mcpRuntimeCatalog.register("prompt", name, definition, nil, nil, handler, true, true)
}

// RemoveMCPPrompt removes a prompt by its catalog name.
func RemoveMCPPrompt(name string) bool { return mcpRuntimeCatalog.remove("prompt", name) }

func (catalog *mcpCatalog) seed(kind string, definition map[string]any) error {
	return catalog.register(kind, "", definition, nil, nil, nil, false, false)
}

func (catalog *mcpCatalog) register(kind, previousName string, definition map[string]any, tool MCPToolHandler, resource MCPResourceHandler, prompt MCPPromptHandler, replace, notify bool) error {
	clone, err := cloneCatalogDefinition(definition)
	if err != nil {
		return err
	}
	name, _ := clone["name"].(string)
	if !validCatalogName(name) {
		return errors.New("MCP catalog name must be 1 to 128 visible ASCII characters without whitespace")
	}
	key := name
	if previousName != "" {
		key = previousName
	}
	if kind == "resource" {
		uri, hasURI := clone["uri"].(string)
		template, hasTemplate := clone["uriTemplate"].(string)
		_, uriPresent := clone["uri"]
		_, templatePresent := clone["uriTemplate"]
		if uriPresent == templatePresent || uriPresent && (!hasURI || !validResourceUpdateURI(uri)) || templatePresent && (!hasTemplate || !validResourceTemplate(template)) {
			return errors.New("MCP resource must declare one valid uri or uriTemplate")
		}
	}
	if kind != "tool" && kind != "resource" && kind != "prompt" {
		return errors.New("unknown MCP catalog kind")
	}

	catalog.mu.Lock()
	var entries map[string]mcpCatalogEntry
	switch kind {
	case "tool":
		entries = catalog.tools
	case "resource":
		entries = catalog.resources
	case "prompt":
		entries = catalog.prompts
	}
	_, exists := entries[key]
	if replace && !exists {
		catalog.mu.Unlock()
		return fmt.Errorf("MCP %s %q is not registered", kind, key)
	}
	if !replace && exists {
		catalog.mu.Unlock()
		return fmt.Errorf("MCP %s %q is already registered", kind, key)
	}
	if kind == "resource" {
		newURI := clone["uri"]
		if newURI == nil {
			newURI = clone["uriTemplate"]
		}
		for existingName, existing := range entries {
			if existingName == key {
				continue
			}
			existingURI := existing.definition["uri"]
			if existingURI == nil {
				existingURI = existing.definition["uriTemplate"]
			}
			if existingURI == newURI {
				catalog.mu.Unlock()
				return fmt.Errorf("MCP resource URI %v is already registered", newURI)
			}
		}
	}
	if previousName != "" && previousName != name {
		if _, collision := entries[name]; collision {
			catalog.mu.Unlock()
			return fmt.Errorf("MCP %s %q is already registered", kind, name)
		}
		delete(entries, previousName)
	}
	catalog.version++
	entries[name] = catalogEntryFor(kind, clone, tool, resource, prompt, catalog.version)
	catalog.mu.Unlock()
	if notify {
		catalog.publishChange()
	}
	return nil
}

func catalogEntryFor(kind string, definition map[string]any, tool MCPToolHandler, resource MCPResourceHandler, prompt MCPPromptHandler, version uint64) mcpCatalogEntry {
	entry := mcpCatalogEntry{definition: definition, version: version}
	switch kind {
	case "tool":
		entry.tool = tool
	case "resource":
		entry.resource = resource
	case "prompt":
		entry.prompt = prompt
	}
	return entry
}

func (catalog *mcpCatalog) remove(kind, name string) bool {
	catalog.mu.Lock()
	var entries map[string]mcpCatalogEntry
	switch kind {
	case "tool":
		entries = catalog.tools
	case "resource":
		entries = catalog.resources
	case "prompt":
		entries = catalog.prompts
	default:
		catalog.mu.Unlock()
		return false
	}
	_, exists := entries[name]
	if exists {
		delete(entries, name)
		catalog.version++
	}
	catalog.mu.Unlock()
	if exists {
		catalog.publishChange()
	}
	return exists
}

func cloneCatalogDefinition(definition map[string]any) (map[string]any, error) {
	if definition == nil {
		return nil, errors.New("MCP catalog definition is required")
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("MCP catalog definition must be JSON serializable: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil || clone == nil {
		return nil, errors.New("MCP catalog definition must be a JSON object")
	}
	return clone, nil
}

func validCatalogName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

var catalogTemplateVariable = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_.-]*)\}`)

func validResourceTemplate(template string) bool {
	if len(catalogTemplateVariable.FindAllStringSubmatch(template, -1)) == 0 {
		return false
	}
	expanded := catalogTemplateVariable.ReplaceAllString(template, "value")
	if strings.ContainsAny(expanded, "{}") || !validResourceUpdateURI(expanded) {
		return false
	}
	return true
}

func (catalog *mcpCatalog) list(kind string) []map[string]any {
	catalog.mu.RLock()
	entries := catalog.entries(kind)
	values := make([]mcpCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry)
	}
	catalog.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool {
		left, _ := values[i].definition["name"].(string)
		right, _ := values[j].definition["name"].(string)
		return left < right
	})
	definitions := make([]map[string]any, 0, len(values))
	for _, entry := range values {
		definitions = append(definitions, entry.definition)
	}
	return definitions
}

// contractFingerprint returns the monotonically increasing local revision and
// a stable digest of the advertised tool, resource, and prompt definitions.
// The digest deliberately excludes handlers and the revision itself.
func (catalog *mcpCatalog) contractFingerprint(features ...map[string]any) (uint64, string, error) {
	catalog.mu.RLock()
	contract := map[string]any{"features": map[string]any{}}
	if len(features) > 0 && features[0] != nil {
		contract["features"] = features[0]
	}
	for _, kind := range []string{"tool", "resource", "prompt"} {
		entries := catalog.entries(kind)
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		definitions := make([]map[string]any, 0, len(names))
		for _, name := range names {
			definition, err := cloneCatalogDefinition(entries[name].definition)
			if err != nil {
				catalog.mu.RUnlock()
				return 0, "", err
			}
			definitions = append(definitions, definition)
		}
		contract[kind+"s"] = definitions
	}
	revision := catalog.version
	catalog.mu.RUnlock()
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(contract)
	if err != nil {
		return 0, "", err
	}
	digest := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return revision, hex.EncodeToString(digest[:]), nil
}

func (catalog *mcpCatalog) entries(kind string) map[string]mcpCatalogEntry {
	switch kind {
	case "tool":
		return catalog.tools
	case "resource":
		return catalog.resources
	case "prompt":
		return catalog.prompts
	default:
		return nil
	}
}

func (catalog *mcpCatalog) tool(name string) (mcpCatalogEntry, bool) {
	catalog.mu.RLock()
	entry, ok := catalog.tools[name]
	catalog.mu.RUnlock()
	return entry, ok
}

func (catalog *mcpCatalog) prompt(name string) (mcpCatalogEntry, bool) {
	catalog.mu.RLock()
	entry, ok := catalog.prompts[name]
	catalog.mu.RUnlock()
	return entry, ok
}

func (catalog *mcpCatalog) hasResourcePattern(pattern string) bool {
	catalog.mu.RLock()
	defer catalog.mu.RUnlock()
	for _, entry := range catalog.resources {
		uri, _ := entry.definition["uri"].(string)
		template, _ := entry.definition["uriTemplate"].(string)
		if uri == pattern || template == pattern {
			return true
		}
	}
	return false
}

func (catalog *mcpCatalog) resource(uri string) (mcpCatalogEntry, map[string]string, bool) {
	catalog.mu.RLock()
	entries := make([]mcpCatalogEntry, 0, len(catalog.resources))
	for _, entry := range catalog.resources {
		entries = append(entries, entry)
	}
	catalog.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		left, _ := entries[i].definition["name"].(string)
		right, _ := entries[j].definition["name"].(string)
		return left < right
	})
	for _, entry := range entries {
		if exact, _ := entry.definition["uri"].(string); exact != "" && exact == uri {
			return entry, map[string]string{}, true
		}
	}
	for _, entry := range entries {
		if template, _ := entry.definition["uriTemplate"].(string); template != "" {
			if values, ok := matchResourceTemplate(template, uri); ok {
				return entry, values, true
			}
		}
	}
	return mcpCatalogEntry{}, nil, false
}

func matchResourceTemplate(template, uri string) (map[string]string, bool) {
	variables := catalogTemplateVariable.FindAllStringSubmatch(template, -1)
	if len(variables) == 0 {
		return nil, false
	}
	var expression strings.Builder
	position := 0
	for _, match := range catalogTemplateVariable.FindAllStringSubmatchIndex(template, -1) {
		expression.WriteString(regexp.QuoteMeta(template[position:match[0]]))
		expression.WriteString("([^/?#]+)")
		position = match[1]
	}
	expression.WriteString(regexp.QuoteMeta(template[position:]))
	compiled, err := regexp.Compile("^" + expression.String() + "$")
	if err != nil {
		return nil, false
	}
	parts := compiled.FindStringSubmatch(uri)
	if parts == nil || len(parts) != len(variables)+1 {
		return nil, false
	}
	values := make(map[string]string, len(variables))
	for index, variable := range variables {
		values[variable[1]] = parts[index+1]
	}
	return values, true
}

func (catalog *mcpCatalog) subscribe() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	catalog.mu.Lock()
	catalog.subscribers[updates] = struct{}{}
	catalog.mu.Unlock()
	var once sync.Once
	return updates, func() {
		once.Do(func() {
			catalog.mu.Lock()
			delete(catalog.subscribers, updates)
			catalog.mu.Unlock()
		})
	}
}

func (catalog *mcpCatalog) publishChange() {
	catalog.mu.RLock()
	for updates := range catalog.subscribers {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	catalog.mu.RUnlock()
}

func requestContext(identity *authContext) *MCPRequestContext {
	if identity == nil {
		return &MCPRequestContext{}
	}
	caller := &MCPRequestContext{Subject: identity.subject, ClientID: identity.clientID}
	for scope := range identity.scopes {
		caller.Scopes = append(caller.Scopes, scope)
	}
	sort.Strings(caller.Scopes)
	return caller
}
