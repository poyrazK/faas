package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	currentProtocol = "2026-07-28"
	legacyProtocol  = "2025-11-25"
	maxRequestBytes = 1 << 20
)

var (
	summaryStyles      = []string{"brief", "technical", "executive"}
	sampleRecordIDs    = []string{"example-1", "example-2", "example-3"}
	sampleRecordOwners = map[string]string{"example-1": "demo-caller-a", "example-2": "demo-caller-b", "example-3": "demo-caller-a"}
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func main() {
	// Host log intake supplies timestamps; protocol events must remain JSON.
	log.SetFlags(0)
	if len(os.Args) > 1 {
		os.Exit(runTaskMigrationCommand(os.Args[1:]))
	}
	role, err := configuredMCPTaskRole()
	if err != nil {
		log.Fatal(err)
	}
	config, err := loadMCPConfig()
	if err != nil {
		log.Fatal(err)
	}
	var auth *authenticator
	if role == "combined" || role == "web" {
		auth, err = newAuthenticator(config)
		if err != nil {
			log.Fatalf("invalid MCP auth configuration: %v", err)
		}
	}
	tasks, err := startMCPTaskRuntime(config, role)
	if err != nil {
		log.Fatalf("could not start durable MCP Tasks: %v", err)
	}
	if tasks != nil {
		defer tasks.close()
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	if role == "worker" || role == "observer" {
		log.Printf("MCP task %s process started", role)
		<-stopped
		return
	}
	address := os.Getenv("MCP_BIND_ADDRESS")
	if address == "" {
		address = "127.0.0.1"
		if os.Getenv("FAAS_APP_ID") != "" {
			address = "0.0.0.0"
		}
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	var draining atomic.Bool
	shutdownDone := make(chan struct{})
	server := &http.Server{
		Addr: address + ":" + port,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if draining.Load() {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "MCP server is draining", http.StatusServiceUnavailable)
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
			handleHTTP(config, auth, tasks, w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Response handlers install a bounded write deadline; task subscriptions
		// clear it and remain open until disconnect or token expiry.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-stopped
		defer close(shutdownDone)
		draining.Store(true)
		tasks.beginDrain()
		taskDone := make(chan struct{})
		go func() { tasks.close(); close(taskDone) }()
		drainSeconds := 10
		if raw := os.Getenv("MCP_DRAIN_TIMEOUT_SECONDS"); raw != "" {
			drainSeconds, _ = strconv.Atoi(raw)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(drainSeconds)*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
		<-taskDone
	}()
	heartbeatContext, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	server.RegisterOnShutdown(stopHeartbeat)
	go startProtocolHeartbeat(heartbeatContext)
	log.Printf("MCP server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}

func handleHTTP(config mcpConfig, auth *authenticator, tasks *mcpTaskRuntime, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", "Origin")
	origins := r.Header.Values("Origin")
	allowed := len(origins) == 0
	if len(origins) == 1 {
		for _, origin := range config.AllowedOrigins {
			if origin == origins[0] {
				allowed = true
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Protocol-Version, Streaming-Status")
				break
			}
		}
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_not_allowed"})
		return
	}
	if r.Method == http.MethodOptions {
		handleCORSPreflight(config, w, r, len(origins) == 1)
		return
	}
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		ready, revision, digest := runtimeCatalogStatus(config, tasks)
		statusCode := http.StatusOK
		status := "ok"
		if !ready {
			statusCode = http.StatusServiceUnavailable
			status = "degraded"
		}
		writeJSON(w, statusCode, map[string]any{
			"ok": ready, "status": status, "instance_id": strings.TrimSpace(os.Getenv("FAAS_INSTANCE_ID")),
			"catalog_revision": revision, "catalog_sha256": digest,
		})
		return
	}
	metadataPath := "/.well-known/oauth-protected-resource" + config.Endpoint
	if auth.mode == "external-oauth" && r.URL.Path == metadataPath && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, auth.metadata())
		return
	}
	if r.URL.Path != config.Endpoint {
		http.NotFound(w, r)
		return
	}
	if ready, _, _ := runtimeCatalogStatus(config, tasks); !ready {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "catalog_contract_mismatch"})
		return
	}
	identity, failure := auth.authenticate(r.Header.Get("Authorization"))
	if failure != nil {
		auth.writeFailure(w, failure)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "request body is missing or exceeds 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
		writeRPCError(w, json.RawMessage("null"), -32600, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}
	if len(request.ID) > 0 {
		var observation *protocolObservation
		w, observation = observeProtocolRequest(w, r, request)
		defer observation.finish()
	}
	switch request.Method {
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(request.Params, &params) == nil && params.Name != "" {
			if failure := auth.toolAllowed(params.Name, identity); failure != nil {
				auth.writeFailure(w, failure)
				return
			}
		}
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(request.Params, &params) == nil && params.URI != "" {
			if failure := auth.resourceAllowed(params.URI, identity); failure != nil {
				auth.writeFailure(w, failure)
				return
			}
		}
	case "prompts/get":
		var params struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(request.Params, &params) == nil && params.Name != "" {
			if failure := auth.promptAllowed(params.Name, identity); failure != nil {
				auth.writeFailure(w, failure)
				return
			}
		}
	case "completion/complete":
		var params struct {
			Ref struct {
				Type string `json:"type"`
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"ref"`
		}
		if json.Unmarshal(request.Params, &params) == nil {
			identifier := params.Ref.Name
			if params.Ref.Type == "ref/resource" {
				identifier = params.Ref.URI
			}
			failure := auth.completionReferenceAllowed(params.Ref.Type, identifier, identity)
			if failure != nil {
				auth.writeFailure(w, failure)
				return
			}
		}
	}
	if request.Method == "subscriptions/listen" && len(request.ID) == 0 {
		writeRPCError(w, json.RawMessage("null"), -32600, "subscriptions/listen requires a JSON-RPC request ID", http.StatusBadRequest)
		return
	}
	if len(request.ID) == 0 {
		// MCP notifications have no response body. They cannot change server state.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !validRPCID(request.ID) {
		writeRPCError(w, json.RawMessage("null"), -32600, "invalid JSON-RPC request ID", http.StatusBadRequest)
		return
	}
	version := r.Header.Get("MCP-Protocol-Version")
	if version == "" {
		version = currentProtocol
	}
	if version != currentProtocol && version != legacyProtocol {
		writeRPCError(w, request.ID, -32600, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	w.Header().Set("MCP-Protocol-Version", version)
	if request.Method == "subscriptions/listen" {
		serveTaskSubscription(w, r, request.ID, request.Params, version, auth, identity, tasks)
		return
	}

	switch request.Method {
	case "server/discover":
		if version != currentProtocol {
			writeRPCError(w, request.ID, -32601, "method not found", http.StatusNotFound)
			return
		}
		writeRPCResult(w, request.ID, map[string]any{
			"supportedVersions": []string{currentProtocol, legacyProtocol},
			"capabilities":      serverCapabilities(tasks, true),
		})
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.ProtocolVersion != currentProtocol && params.ProtocolVersion != legacyProtocol {
			writeRPCError(w, request.ID, -32602, "unsupported protocol version", http.StatusBadRequest)
			return
		}
		writeRPCResult(w, request.ID, map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    serverCapabilities(tasks, params.ProtocolVersion == currentProtocol),
			"serverInfo":      map[string]string{"name": "gregale-mcp-go", "version": "1.0.0"},
		})
	case "ping":
		writeRPCResult(w, request.ID, map[string]any{})
	case "tools/list":
		writeRPCResult(w, request.ID, map[string]any{"tools": auth.visibleTools(identity)})
	case "tools/call":
		callTool(w, r, request.ID, request.Params, auth, identity, tasks, version)
	case "resources/list":
		writeRPCResult(w, request.ID, map[string]any{"resources": auth.visibleResources(identity)})
	case "resources/templates/list":
		templates := auth.visibleResourceTemplates(identity)
		if tasks != nil && auth.resourceAllowed("task://tasks/{taskId}", identity) == nil {
			templates = append(templates, taskResourceTemplate())
		}
		writeRPCResult(w, request.ID, map[string]any{"resourceTemplates": templates})
	case "resources/read":
		readResource(w, r, request.ID, request.Params, auth, identity, tasks)
	case "prompts/list":
		writeRPCResult(w, request.ID, map[string]any{"prompts": auth.visiblePrompts(identity)})
	case "prompts/get":
		getPrompt(w, r, request.ID, request.Params, auth, identity)
	case "completion/complete":
		complete(w, request.ID, request.Params, auth, identity)
	case "tasks/get":
		serveTaskGet(w, r, request.ID, request.Params, auth, identity, tasks)
	case "tasks/cancel":
		serveTaskCancel(w, r, request.ID, request.Params, auth, identity, tasks)
	case "tasks/update":
		serveTaskUpdate(w, r, request.ID, request.Params, auth, identity, tasks)
	default:
		writeRPCError(w, request.ID, -32601, "method not found", http.StatusNotFound)
	}
}

func runtimeCatalogStatus(config mcpConfig, tasks *mcpTaskRuntime) (bool, uint64, string) {
	var authConfig map[string]json.RawMessage
	_ = json.Unmarshal(config.Auth, &authConfig)
	authPolicy := make(map[string]any, 3)
	for _, field := range []string{"tool_scopes", "resource_scopes", "prompt_scopes"} {
		var value any
		if raw := authConfig[field]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &value)
		}
		authPolicy[field] = value
	}
	features := map[string]any{
		"tasks_enabled": tasks != nil,
		"auth_policy":   authPolicy,
	}
	revision, digest, err := mcpRuntimeCatalog.contractFingerprint(features)
	if err != nil {
		return false, revision, ""
	}
	return config.CatalogExpectedSHA256 == "" || config.CatalogExpectedSHA256 == digest, revision, digest
}

func serverCapabilities(tasks *mcpTaskRuntime, modern bool) map[string]any {
	resources := map[string]any{"listChanged": true}
	if modern {
		resources["subscribe"] = true
	}
	capabilities := map[string]any{
		"tools":       map[string]any{"listChanged": true},
		"resources":   resources,
		"prompts":     map[string]any{"listChanged": true},
		"completions": map[string]any{},
	}
	if tasks != nil && modern {
		capabilities["extensions"] = map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}}
	}
	return capabilities
}

var toolDefinitions = []map[string]any{
	{
		"name": "greet", "description": "Return a greeting.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}},
			"required":   []string{"name"},
		},
		"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
	},
	{
		"name": "stream_demo", "description": "Send three progress events over one second.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
		"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
	},
	{
		"name": "add", "description": "Add two finite numbers.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"a": map[string]string{"type": "number"}, "b": map[string]string{"type": "number"}},
			"required":   []string{"a", "b"},
		},
		"outputSchema": map[string]any{"type": "object", "properties": map[string]any{"sum": map[string]string{"type": "number"}}, "required": []string{"sum"}},
		"annotations":  map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
	},
	{
		"name": "build_report", "description": "Build a harmless sample report; supports durable MCP Tasks when configured.",
		"inputSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"report": map[string]any{"type": "string", "minLength": 1, "maxLength": 80}, "steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "request_approval": map[string]any{"type": "boolean", "description": "Pause the durable task for client approval before generating the report."}}, "required": []string{"report", "steps"}},
		"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
	},
}

var resourceDefinitions = []map[string]any{
	{
		"uri": "greeting://welcome", "name": "welcome", "title": "Welcome",
		"description": "A public example resource.", "mimeType": "text/plain",
	},
}

var resourceTemplateDefinitions = []map[string]any{
	{
		"uriTemplate": "customer://records/{recordId}", "name": "customer_record", "title": "Customer record",
		"description": "Example customer-record resource; enforce ownership in your own data lookup.", "mimeType": "application/json",
	},
}

var promptDefinitions = []map[string]any{
	{
		"name": "summarize", "title": "Summarize text", "description": "Create a concise summary request.",
		"arguments": []map[string]any{
			{"name": "text", "description": "Text to summarize.", "required": true},
			{"name": "style", "description": "Optional summary style: brief, technical, or executive.", "required": false},
		},
	},
}

func callTool(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime, protocol string) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Meta      struct {
			ProgressToken      json.RawMessage `json:"progressToken"`
			ClientCapabilities map[string]any  `json:"io.modelcontextprotocol/clientCapabilities"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		writeRPCError(w, id, -32602, "invalid tool call parameters", http.StatusBadRequest)
		return
	}
	if failure := auth.toolAllowed(params.Name, identity); failure != nil {
		auth.writeFailure(w, failure)
		return
	}
	entry, registered := mcpRuntimeCatalog.tool(params.Name)
	if !registered {
		writeToolError(w, id, "unknown tool")
		return
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	if tasks != nil && protocol == currentProtocol && clientSupportsMcpTasks(params.Meta.ClientCapabilities) {
		if _, registered := tasks.taskHandlers[params.Name]; registered {
			if params.Name == "build_report" && params.Arguments["request_approval"] == true && !containsString(taskInputMethodsFor(params.Meta.ClientCapabilities), "elicitation/create:form") {
				writeToolError(w, id, "request_approval requires client support for elicitation form requests")
				return
			}
			result, err := tasks.create(r.Context(), params.Name, params.Arguments, identity, params.Meta.ClientCapabilities)
			if err != nil {
				writeToolError(w, id, "invalid task arguments or task queue unavailable")
				return
			}
			writeRPCResult(w, id, result)
			return
		}
	}
	if entry.tool != nil {
		result, err := entry.tool(r.Context(), params.Arguments, requestContext(identity))
		if err != nil || result == nil {
			writeToolError(w, id, "tool execution failed")
			return
		}
		if _, err := json.Marshal(result); err != nil {
			writeToolError(w, id, "tool handler returned an invalid result")
			return
		}
		writeRPCResult(w, id, result)
		return
	}
	switch params.Name {
	case "greet":
		name, ok := params.Arguments["name"].(string)
		if len(params.Arguments) != 1 || !ok || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 100 {
			writeToolError(w, id, "name must be a non-empty string of at most 100 bytes")
			return
		}
		writeRPCResult(w, id, map[string]any{"content": []map[string]string{{"type": "text", "text": "Hello, " + name + "!"}}})
	case "add":
		a, aOK := params.Arguments["a"].(float64)
		b, bOK := params.Arguments["b"].(float64)
		if len(params.Arguments) != 2 || !aOK || !bOK || math.IsNaN(a) || math.IsInf(a, 0) || math.IsNaN(b) || math.IsInf(b, 0) {
			writeToolError(w, id, "a and b must be finite numbers")
			return
		}
		sum := a + b
		if math.IsInf(sum, 0) || math.IsNaN(sum) {
			writeToolError(w, id, "sum exceeds the finite number range")
			return
		}
		writeRPCResult(w, id, map[string]any{
			"content":           []map[string]string{{"type": "text", "text": fmt.Sprint(sum)}},
			"structuredContent": map[string]float64{"sum": sum},
		})
	case "stream_demo":
		if len(params.Arguments) != 0 {
			writeToolError(w, id, "stream_demo does not accept arguments")
			return
		}
		if len(params.Meta.ProgressToken) > 0 && !validProgressToken(params.Meta.ProgressToken) {
			writeRPCError(w, id, -32602, "progressToken must be a string or finite number", http.StatusBadRequest)
			return
		}
		streamDemo(w, r, id, params.Meta.ProgressToken)
	case "build_report":
		report, reportOK := params.Arguments["report"].(string)
		stepsValue, stepsOK := params.Arguments["steps"].(float64)
		requireApproval, approvalPresent := params.Arguments["request_approval"].(bool)
		steps := int(stepsValue)
		if (len(params.Arguments) != 2 && len(params.Arguments) != 3) || (len(params.Arguments) == 3 && !approvalPresent) || !reportOK || strings.TrimSpace(report) == "" || len(utf16.Encode([]rune(report))) > 80 || !stepsOK || float64(steps) != stepsValue || steps < 1 || steps > 20 {
			writeToolError(w, id, "report must be 1 to 80 characters and steps must be an integer from 1 to 20")
			return
		}
		if requireApproval {
			writeToolError(w, id, "request_approval requires durable MCP Tasks and client elicitation support")
			return
		}
		if !waitForProgress(r.Context(), time.Duration(steps)*200*time.Millisecond) {
			return
		}
		writeRPCResult(w, id, map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("Report %s is ready after %d steps.", report, steps)}}})
	default:
		writeToolError(w, id, "unknown tool")
	}
}

func clientSupportsMcpTasks(capabilities map[string]any) bool {
	extensions, _ := capabilities["extensions"].(map[string]any)
	value, ok := extensions["io.modelcontextprotocol/tasks"]
	if !ok || value == nil {
		return false
	}
	switch enabled := value.(type) {
	case bool:
		return enabled
	case string:
		return enabled != ""
	case float64:
		return enabled != 0 && !math.IsNaN(enabled)
	}
	return true
}

func validProgressToken(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch token := value.(type) {
	case string:
		return true
	case float64:
		return !math.IsNaN(token) && !math.IsInf(token, 0)
	default:
		return false
	}
}

func acceptsEventStream(header string) bool {
	for _, entry := range strings.Split(header, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
			continue
		}
		if quality, present := parameters["q"]; present {
			parsed, err := strconv.ParseFloat(quality, 64)
			if err != nil || parsed <= 0 || parsed > 1 {
				continue
			}
		}
		return true
	}
	return false
}

func streamDemo(w http.ResponseWriter, r *http.Request, id, progressToken json.RawMessage) {
	flusher, canFlush := w.(http.Flusher)
	stream := len(progressToken) > 0 && acceptsEventStream(r.Header.Get("Accept")) && canFlush
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
	}

	for step := 1; step <= 3; step++ {
		if r.Context().Err() != nil {
			return
		}
		if stream {
			notification := map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/progress",
				"params":  map[string]any{"progressToken": progressToken, "progress": step, "total": 3},
			}
			if err := writeSSEMessage(w, notification); err != nil {
				return
			}
			flusher.Flush()
		}
		if !waitForProgress(r.Context(), 400*time.Millisecond) {
			return
		}
	}

	result := map[string]any{"content": []map[string]string{{"type": "text", "text": "Streaming complete."}}}
	if stream {
		_ = writeSSEMessage(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: protocolResult(w, result)})
		flusher.Flush()
		return
	}
	writeRPCResult(w, id, result)
}

func waitForProgress(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func writeSSEMessage(w io.Writer, message any) error {
	noteProtocolResponse(w, message)
	data, err := json.Marshal(message)
	if err != nil {
		noteProtocolEncodingFailure(w)
		return err
	}
	_, err = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	return err
}

func readResource(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.URI == "" {
		writeRPCError(w, id, -32602, "resource URI is required", http.StatusBadRequest)
		return
	}
	if failure := auth.resourceAllowed(params.URI, identity); failure != nil {
		auth.writeFailure(w, failure)
		return
	}
	if taskID, ok := taskURIID(params.URI); ok && tasks != nil {
		_, task, err := tasks.get(r.Context(), taskID, identity)
		if err != nil || auth.toolAllowed(task.ToolName, identity) != nil {
			writeRPCError(w, id, -32002, "resource not found", http.StatusNotFound)
			return
		}
		content, _ := json.Marshal(tasks.wire(task))
		writeRPCResult(w, id, map[string]any{"contents": []map[string]string{{"uri": params.URI, "mimeType": "application/json", "text": string(content)}}})
		return
	}
	entry, variables, registered := mcpRuntimeCatalog.resource(params.URI)
	if !registered {
		writeRPCError(w, id, -32002, "resource not found", http.StatusNotFound)
		return
	}
	if entry.resource != nil {
		contents, err := entry.resource(r.Context(), params.URI, variables, requestContext(identity))
		if err != nil || contents == nil {
			writeRPCError(w, id, -32603, "resource read failed", http.StatusInternalServerError)
			return
		}
		if _, err := json.Marshal(contents); err != nil {
			writeRPCError(w, id, -32603, "resource handler returned an invalid result", http.StatusInternalServerError)
			return
		}
		writeRPCResult(w, id, map[string]any{"contents": contents})
		return
	}
	if params.URI == "greeting://welcome" {
		writeRPCResult(w, id, map[string]any{"contents": []map[string]string{{
			"uri": params.URI, "mimeType": "text/plain", "text": "Welcome to the Gregale MCP starter.",
		}}})
		return
	}
	recordID, ok := customerRecordID(params.URI)
	owner, known := sampleRecordOwners[recordID]
	if !ok || !known || (auth.mode != "open" && (identity == nil || identity.subject != owner)) {
		writeRPCError(w, id, -32002, "resource not found", http.StatusNotFound)
		return
	}
	content, err := json.Marshal(map[string]string{"recordId": recordID, "status": "example"})
	if err != nil {
		writeRPCError(w, id, -32603, "could not encode resource", http.StatusInternalServerError)
		return
	}
	writeRPCResult(w, id, map[string]any{"contents": []map[string]string{{
		"uri": params.URI, "mimeType": "application/json", "text": string(content),
	}}})
}

func serveTaskGet(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) {
	if tasks == nil {
		writeRPCError(w, id, -32601, "method not found", http.StatusNotFound)
		return
	}
	var params struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || !taskIDPattern.MatchString(params.TaskID) {
		writeRPCError(w, id, -32602, "taskId must be a UUID", http.StatusBadRequest)
		return
	}
	result, task, err := tasks.get(r.Context(), params.TaskID, identity)
	if err != nil || auth.toolAllowed(task.ToolName, identity) != nil {
		writeRPCError(w, id, -32002, "task not found", http.StatusNotFound)
		return
	}
	writeRPCResult(w, id, result)
}

func serveTaskCancel(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) {
	if tasks == nil {
		writeRPCError(w, id, -32601, "method not found", http.StatusNotFound)
		return
	}
	var params struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || !taskIDPattern.MatchString(params.TaskID) {
		writeRPCError(w, id, -32602, "taskId must be a UUID", http.StatusBadRequest)
		return
	}
	_, task, err := tasks.get(r.Context(), params.TaskID, identity)
	if err != nil || auth.toolAllowed(task.ToolName, identity) != nil || tasks.cancelTask(r.Context(), params.TaskID, identity) != nil {
		writeRPCError(w, id, -32002, "task not found", http.StatusNotFound)
		return
	}
	writeRPCResult(w, id, map[string]any{"resultType": "complete"})
}

func serveTaskUpdate(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) {
	if tasks == nil {
		writeRPCError(w, id, -32601, "method not found", http.StatusNotFound)
		return
	}
	var params struct {
		TaskID         string         `json:"taskId"`
		InputResponses map[string]any `json:"inputResponses"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || !taskIDPattern.MatchString(params.TaskID) {
		writeRPCError(w, id, -32602, "taskId must be a UUID", http.StatusBadRequest)
		return
	}
	if params.InputResponses == nil {
		writeRPCError(w, id, -32602, "Task input responses must be an object", http.StatusBadRequest)
		return
	}
	_, task, err := tasks.get(r.Context(), params.TaskID, identity)
	if err != nil || auth.toolAllowed(task.ToolName, identity) != nil {
		writeRPCError(w, id, -32002, "task not found", http.StatusNotFound)
		return
	}
	if err := tasks.updateInputs(r.Context(), params.TaskID, identity, params.InputResponses); err != nil {
		if errors.Is(err, errMCPTaskNotFound) {
			writeRPCError(w, id, -32002, "task not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, errInvalidMCPTaskInput) {
			writeRPCError(w, id, -32602, err.Error(), http.StatusBadRequest)
			return
		}
		writeRPCError(w, id, -32603, "could not update MCP task", http.StatusInternalServerError)
		return
	}
	writeRPCResult(w, id, map[string]any{"resultType": "complete"})
}

func customerRecordID(uri string) (string, bool) {
	const prefix = "customer://records/"
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	recordID := strings.TrimPrefix(uri, prefix)
	if recordID == "" || strings.ContainsAny(recordID, "/?#") {
		return "", false
	}
	return recordID, true
}

func configuredResourceURI(uri string) bool {
	_, _, registered := mcpRuntimeCatalog.resource(uri)
	return registered
}

func getPrompt(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, auth *authenticator, identity *authContext) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		writeRPCError(w, id, -32602, "prompt name is required", http.StatusBadRequest)
		return
	}
	if failure := auth.promptAllowed(params.Name, identity); failure != nil {
		auth.writeFailure(w, failure)
		return
	}
	entry, registered := mcpRuntimeCatalog.prompt(params.Name)
	if !registered {
		writeRPCError(w, id, -32602, "prompt not found", http.StatusBadRequest)
		return
	}
	if entry.prompt != nil {
		if params.Arguments == nil {
			params.Arguments = map[string]any{}
		}
		result, err := entry.prompt(r.Context(), params.Arguments, requestContext(identity))
		if err != nil || result == nil {
			writeRPCError(w, id, -32603, "prompt execution failed", http.StatusInternalServerError)
			return
		}
		if _, err := json.Marshal(result); err != nil {
			writeRPCError(w, id, -32603, "prompt handler returned an invalid result", http.StatusInternalServerError)
			return
		}
		writeRPCResult(w, id, result)
		return
	}
	if params.Name != "summarize" {
		writeRPCError(w, id, -32602, "prompt not found", http.StatusBadRequest)
		return
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	textValue, textOK := params.Arguments["text"].(string)
	styleValue, stylePresent := params.Arguments["style"]
	style, styleOK := styleValue.(string)
	if !textOK || textValue == "" || utf8.RuneCountInString(textValue) > 4000 || len(params.Arguments) > 2 ||
		(stylePresent && (!styleOK || !supportedSummaryStyle(style))) {
		writeRPCError(w, id, -32602, "text must be 1 to 4000 characters and style must be brief, technical, or executive", http.StatusBadRequest)
		return
	}
	description := "Ask for a concise summary."
	instruction := "Summarize this text concisely:"
	if stylePresent {
		description = "Ask for a " + style + " summary."
		instruction = "Summarize this text in a " + style + " style:"
	}
	writeRPCResult(w, id, map[string]any{
		"description": description,
		"messages": []map[string]any{{
			"role": "user", "content": map[string]string{"type": "text", "text": instruction + "\n\n" + textValue},
		}},
	})
}

func supportedSummaryStyle(style string) bool {
	for _, candidate := range summaryStyles {
		if style == candidate {
			return true
		}
	}
	return false
}

func complete(w http.ResponseWriter, id, rawParams json.RawMessage, auth *authenticator, identity *authContext) {
	var params struct {
		Ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"ref"`
		Argument struct {
			Name  string  `json:"name"`
			Value *string `json:"value"`
		} `json:"argument"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Argument.Value == nil || params.Argument.Name == "" {
		writeRPCError(w, id, -32602, "completion reference and argument are required", http.StatusBadRequest)
		return
	}
	var candidates []string
	switch params.Ref.Type {
	case "ref/prompt":
		if params.Ref.Name != "summarize" || params.Argument.Name != "style" || auth.promptAllowed(params.Ref.Name, identity) != nil {
			writeRPCError(w, id, -32602, "unknown completion reference or argument", http.StatusBadRequest)
			return
		}
		if _, registered := mcpRuntimeCatalog.prompt(params.Ref.Name); !registered {
			writeRPCError(w, id, -32602, "unknown completion reference or argument", http.StatusBadRequest)
			return
		}
		candidates = summaryStyles
	case "ref/resource":
		if params.Ref.URI != "customer://records/{recordId}" || params.Argument.Name != "recordId" || !mcpRuntimeCatalog.hasResourcePattern(params.Ref.URI) || auth.resourceAllowed(params.Ref.URI, identity) != nil {
			writeRPCError(w, id, -32602, "unknown completion reference or argument", http.StatusBadRequest)
			return
		}
		for _, recordID := range sampleRecordIDs {
			if auth.mode == "open" || identity != nil && identity.subject == sampleRecordOwners[recordID] {
				candidates = append(candidates, recordID)
			}
		}
	default:
		writeRPCError(w, id, -32602, "unknown completion reference", http.StatusBadRequest)
		return
	}
	prefix := strings.ToLower(*params.Argument.Value)
	values := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.HasPrefix(strings.ToLower(candidate), prefix) {
			values = append(values, candidate)
		}
	}
	writeRPCResult(w, id, map[string]any{
		"completion": map[string]any{"values": values, "total": len(values), "hasMore": false},
	})
}

func writeToolError(w http.ResponseWriter, id json.RawMessage, message string) {
	writeRPCResult(w, id, map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": message}}})
}

func validRPCID(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch id := value.(type) {
	case string:
		return true
	case float64:
		return !math.IsNaN(id) && !math.IsInf(id, 0)
	default:
		return false
	}
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: protocolResult(w, result)})
}

// Modern successful results and caller-specific cache metadata are required on the wire.
func protocolResult(w http.ResponseWriter, result any) any {
	if w.Header().Get("MCP-Protocol-Version") != currentProtocol {
		return result
	}
	fields, ok := result.(map[string]any)
	if !ok {
		return result
	}
	wire := make(map[string]any)
	for key, value := range fields {
		wire[key] = value
	}
	if _, ok := wire["resultType"]; !ok {
		wire["resultType"] = "complete"
	}
	for _, key := range []string{"tools", "resources", "resourceTemplates", "prompts", "contents", "supportedVersions"} {
		if _, ok := wire[key]; ok {
			wire["ttlMs"] = 0
			wire["cacheScope"] = "private"
			break
		}
	}
	return wire
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, status int) {
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	noteProtocolResponse(w, value)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		noteProtocolEncodingFailure(w)
		log.Printf("could not write JSON response: %v", err)
	}
}
