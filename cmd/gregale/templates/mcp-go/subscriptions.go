package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	mcpProtocolVersionMetaKey = "io.modelcontextprotocol/protocolVersion"
	mcpClientCapabilitiesKey  = "io.modelcontextprotocol/clientCapabilities"
	mcpSubscriptionIDMetaKey  = "io.modelcontextprotocol/subscriptionId"
	mcpServerInfoMetaKey      = "io.modelcontextprotocol/serverInfo"
	mcpTasksExtensionID       = "io.modelcontextprotocol/tasks"
	maxTaskSubscriptionIDs    = 64
	maxTaskSubscriptions      = 64
	maxWatchedTaskIDs         = 512
	maxSubscriptionStreams    = 128
	maxResourceSubscriptions  = 64
	maxResourceURIBytes       = 16 * 1024
	taskSubscriptionKeepAlive = 15 * time.Second
)

var subscriptionStreamSlots = make(chan struct{}, maxSubscriptionStreams)

func (runtime *mcpTaskRuntime) reserveTaskSubscription() bool {
	runtime.subscriptionMu.Lock()
	defer runtime.subscriptionMu.Unlock()
	if runtime.subscriptions >= maxTaskSubscriptions {
		return false
	}
	runtime.subscriptions++
	return true
}

func (runtime *mcpTaskRuntime) reserveWatchedTaskIDs(count int) bool {
	runtime.subscriptionMu.Lock()
	defer runtime.subscriptionMu.Unlock()
	if count < 0 || runtime.watchedTasks+count > maxWatchedTaskIDs {
		return false
	}
	runtime.watchedTasks += count
	return true
}

func (runtime *mcpTaskRuntime) releaseTaskSubscription(taskCount int) {
	runtime.subscriptionMu.Lock()
	defer runtime.subscriptionMu.Unlock()
	if taskCount > runtime.watchedTasks {
		taskCount = runtime.watchedTasks
	}
	runtime.watchedTasks -= taskCount
	if runtime.subscriptions > 0 {
		runtime.subscriptions--
	}
}

func (runtime *mcpTaskRuntime) releaseWatchedTaskIDs(count int) {
	runtime.subscriptionMu.Lock()
	defer runtime.subscriptionMu.Unlock()
	if count > runtime.watchedTasks {
		count = runtime.watchedTasks
	}
	runtime.watchedTasks -= count
}

func serveTaskSubscription(w http.ResponseWriter, r *http.Request, id, rawParams json.RawMessage, protocol string, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) {
	if !validTaskSubscriptionID(id) {
		writeRPCError(w, json.RawMessage("null"), -32600, "invalid JSON-RPC request ID", http.StatusBadRequest)
		return
	}
	if protocol != currentProtocol || r.Header.Get("MCP-Protocol-Version") != protocol || r.Header.Get("Mcp-Method") != "subscriptions/listen" || !acceptsEventStream(r.Header.Get("Accept")) {
		writeRPCError(w, id, -32602, "subscriptions/listen requires the current protocol, MCP-Method header, and text/event-stream", http.StatusBadRequest)
		return
	}
	var params struct {
		Notifications map[string]json.RawMessage `json:"notifications"`
		Meta          map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Notifications == nil || params.Meta == nil {
		writeRPCError(w, id, -32602, "notifications and _meta must be objects", http.StatusBadRequest)
		return
	}
	var metadataProtocol string
	if json.Unmarshal(params.Meta[mcpProtocolVersionMetaKey], &metadataProtocol) != nil || metadataProtocol != protocol {
		writeRPCError(w, id, -32602, "subscription protocol metadata must match MCP-Protocol-Version", http.StatusBadRequest)
		return
	}

	listFilters := make(map[string]bool)
	for _, key := range []string{"toolsListChanged", "resourcesListChanged", "promptsListChanged"} {
		if raw, present := params.Notifications[key]; present {
			var enabled bool
			if json.Unmarshal(raw, &enabled) != nil {
				writeRPCError(w, id, -32602, "list-change filters must be booleans", http.StatusBadRequest)
				return
			}
			listFilters[key] = enabled
		}
	}

	requestedIDs := []string{}
	hasTaskFilter := false
	if rawIDs, present := params.Notifications["taskIds"]; present {
		hasTaskFilter = true
		var values []string
		if json.Unmarshal(rawIDs, &values) != nil || values == nil || len(values) > maxTaskSubscriptionIDs {
			writeRPCError(w, id, -32602, "notifications.taskIds must contain at most 64 task UUIDs", http.StatusBadRequest)
			return
		}
		seen := make(map[string]struct{}, len(values))
		for _, taskID := range values {
			if !taskIDPattern.MatchString(taskID) {
				writeRPCError(w, id, -32602, "notifications.taskIds must contain at most 64 task UUIDs", http.StatusBadRequest)
				return
			}
			taskID = strings.ToLower(taskID)
			if _, exists := seen[taskID]; exists {
				continue
			}
			seen[taskID] = struct{}{}
			requestedIDs = append(requestedIDs, taskID)
		}
	}
	requestedResourceURIs := []string{}
	hasResourceFilter := false
	if rawURIs, present := params.Notifications["resourceSubscriptions"]; present {
		hasResourceFilter = true
		var values []string
		if json.Unmarshal(rawURIs, &values) != nil || values == nil || len(values) > maxResourceSubscriptions {
			writeRPCError(w, id, -32602, "notifications.resourceSubscriptions must contain at most 64 resource URIs", http.StatusBadRequest)
			return
		}
		seen := make(map[string]struct{}, len(values))
		for _, uri := range values {
			if !validResourceUpdateURI(uri) {
				writeRPCError(w, id, -32602, "notifications.resourceSubscriptions must contain at most 64 resource URIs", http.StatusBadRequest)
				return
			}
			if _, exists := seen[uri]; exists {
				continue
			}
			seen[uri] = struct{}{}
			requestedResourceURIs = append(requestedResourceURIs, uri)
		}
	}
	if len(requestedIDs) > 0 {
		var capabilities map[string]any
		if json.Unmarshal(params.Meta[mcpClientCapabilitiesKey], &capabilities) != nil || !clientSupportsMcpTasks(capabilities) {
			writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{
				Code: -32021, Message: "Missing required client capability",
				Data: map[string]any{"requiredCapabilities": map[string]any{"extensions": map[string]any{mcpTasksExtensionID: map[string]any{}}}},
			}})
			return
		}
	}
	select {
	case subscriptionStreamSlots <- struct{}{}:
		defer func() { <-subscriptionStreamSlots }()
	default:
		writeRPCError(w, id, -32603, "subscription limit reached", http.StatusTooManyRequests)
		return
	}

	type snapshot struct {
		params      map[string]any
		fingerprint string
	}
	requestedSet := make(map[string]struct{}, len(requestedIDs))
	for _, taskID := range requestedIDs {
		requestedSet[taskID] = struct{}{}
	}
	resourceTaskIDs := make(map[string]string)
	resourceURIsByTaskID := make(map[string][]string)
	genericResourceURIs := make(map[string]struct{})
	for _, uri := range requestedResourceURIs {
		if auth.resourceAllowed(uri, identity) != nil {
			continue
		}
		if strings.HasPrefix(uri, "task://tasks/") {
			taskID := taskIDFromResourceURI(uri)
			if taskID == "" || tasks == nil {
				continue
			}
			_, task, err := tasks.get(r.Context(), taskID, identity)
			if err != nil || auth.toolAllowed(task.ToolName, identity) != nil {
				continue
			}
			resourceTaskIDs[uri] = taskID
			resourceURIsByTaskID[taskID] = append(resourceURIsByTaskID[taskID], uri)
			continue
		}
		if !configuredResourceURI(uri) {
			continue
		}
		genericResourceURIs[uri] = struct{}{}
	}
	allTaskIDs := append([]string(nil), requestedIDs...)
	seenTaskIDs := make(map[string]struct{}, len(allTaskIDs))
	for _, taskID := range allTaskIDs {
		seenTaskIDs[taskID] = struct{}{}
	}
	for _, uri := range requestedResourceURIs {
		taskID, ok := resourceTaskIDs[uri]
		if !ok {
			continue
		}
		if _, exists := seenTaskIDs[taskID]; exists {
			continue
		}
		seenTaskIDs[taskID] = struct{}{}
		allTaskIDs = append(allTaskIDs, taskID)
	}
	if len(allTaskIDs) > maxTaskSubscriptionIDs {
		writeRPCError(w, id, -32602, "task and resource subscription filters exceed 64 distinct task IDs", http.StatusBadRequest)
		return
	}
	taskSubscriptionReserved := false
	reservedIDs := 0
	if tasks != nil && len(allTaskIDs) > 0 {
		if !tasks.reserveTaskSubscription() {
			writeRPCError(w, id, -32603, "task subscription limit reached", http.StatusTooManyRequests)
			return
		}
		taskSubscriptionReserved = true
		defer func() {
			if taskSubscriptionReserved {
				tasks.releaseTaskSubscription(reservedIDs)
			}
		}()
	}
	current := make(map[string]snapshot, len(allTaskIDs))
	acceptedIDs := make([]string, 0, len(allTaskIDs))
	if tasks != nil {
		for _, taskID := range allTaskIDs {
			value, task, err := tasks.get(r.Context(), taskID, identity)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				writeRPCError(w, id, -32603, "could not read task subscription state", http.StatusInternalServerError)
				return
			}
			if auth.toolAllowed(task.ToolName, identity) != nil {
				continue
			}
			detail, fingerprint, err := taskSubscriptionDetail(value)
			if err != nil {
				writeRPCError(w, id, -32603, "could not encode task subscription state", http.StatusInternalServerError)
				return
			}
			current[taskID] = snapshot{params: detail, fingerprint: fingerprint}
			acceptedIDs = append(acceptedIDs, taskID)
		}
	}
	acceptedSet := make(map[string]struct{}, len(acceptedIDs))
	for _, taskID := range acceptedIDs {
		acceptedSet[taskID] = struct{}{}
	}
	acceptedResourceURIs := make([]string, 0, len(resourceTaskIDs))
	for _, uri := range requestedResourceURIs {
		if _, generic := genericResourceURIs[uri]; generic {
			acceptedResourceURIs = append(acceptedResourceURIs, uri)
			continue
		}
		if taskID, ok := resourceTaskIDs[uri]; ok {
			if _, accepted := acceptedSet[taskID]; accepted {
				acceptedResourceURIs = append(acceptedResourceURIs, uri)
			} else {
				delete(resourceTaskIDs, uri)
			}
		}
	}
	hasGenericResourceSubscriptions := false
	for _, uri := range acceptedResourceURIs {
		if _, generic := genericResourceURIs[uri]; generic {
			hasGenericResourceSubscriptions = true
			break
		}
	}
	resourceUpdateEvents := make(chan string, maxResourceSubscriptions)
	pendingResourceUpdates := make(map[string]struct{}, len(acceptedResourceURIs))
	var pendingResourceUpdatesMu sync.Mutex
	resourceUpdateUnsubscribes := make([]func(), 0, len(acceptedResourceURIs))
	defer func() {
		for _, unsubscribe := range resourceUpdateUnsubscribes {
			unsubscribe()
		}
	}()
	for _, uri := range acceptedResourceURIs {
		uri := uri
		unsubscribe := subscribeResourceUpdate(uri, func() {
			if !strings.HasPrefix(uri, "task://tasks/") && (auth.resourceAllowed(uri, identity) != nil || !configuredResourceURI(uri)) {
				return
			}
			pendingResourceUpdatesMu.Lock()
			if _, pending := pendingResourceUpdates[uri]; pending {
				pendingResourceUpdatesMu.Unlock()
				return
			}
			pendingResourceUpdates[uri] = struct{}{}
			pendingResourceUpdatesMu.Unlock()
			select {
			case resourceUpdateEvents <- uri:
			default:
				pendingResourceUpdatesMu.Lock()
				delete(pendingResourceUpdates, uri)
				pendingResourceUpdatesMu.Unlock()
			}
		})
		if unsubscribe != nil {
			resourceUpdateUnsubscribes = append(resourceUpdateUnsubscribes, unsubscribe)
		}
	}
	if taskSubscriptionAuthExpired(identity) {
		writeRPCError(w, id, -32000, "authentication expired during subscription setup", http.StatusUnauthorized)
		return
	}
	if len(acceptedIDs) > 0 && tasks != nil {
		if !tasks.reserveWatchedTaskIDs(len(acceptedIDs)) {
			writeRPCError(w, id, -32603, "watched task limit reached", http.StatusTooManyRequests)
			return
		}
		reservedIDs = len(acceptedIDs)
	}
	catalogUpdates, unsubscribeCatalog := mcpRuntimeCatalog.subscribe()
	defer unsubscribeCatalog()
	listFingerprints := make(map[string]string)
	for _, key := range []string{"toolsListChanged", "resourcesListChanged", "promptsListChanged"} {
		if !listFilters[key] {
			continue
		}
		fingerprint, err := subscriptionCatalogFingerprint(key, auth, identity, tasks)
		if err != nil {
			writeRPCError(w, id, -32603, "could not encode MCP catalog state", http.StatusInternalServerError)
			return
		}
		listFingerprints[key] = fingerprint
	}

	// Ordinary requests have a bounded write deadline; SSE subscriptions clear it.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCError(w, id, -32603, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flushSSE := func(message any) error {
		if taskSubscriptionAuthExpired(identity) {
			return errors.New("subscription authentication expired")
		}
		if err := writeSSEMessage(w, message); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	acknowledged := map[string]any{}
	if hasTaskFilter && len(requestedIDs) > 0 {
		visibleRequestedIDs := make([]string, 0, len(requestedIDs))
		for _, taskID := range requestedIDs {
			if _, accepted := acceptedSet[taskID]; accepted {
				visibleRequestedIDs = append(visibleRequestedIDs, taskID)
			}
		}
		acknowledged["taskIds"] = visibleRequestedIDs
	}
	if hasResourceFilter && len(acceptedResourceURIs) > 0 {
		acknowledged["resourceSubscriptions"] = acceptedResourceURIs
	}
	for key, enabled := range listFilters {
		if enabled {
			acknowledged[key] = true
		}
	}
	if err := flushSSE(map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{
		"notifications": acknowledged,
		"_meta":         map[string]any{mcpSubscriptionIDMetaKey: id},
	}}); err != nil {
		return
	}
	for _, taskID := range acceptedIDs {
		if _, requested := requestedSet[taskID]; !requested {
			continue
		}
		if err := flushSSE(taskSubscriptionNotification(id, current[taskID].params)); err != nil {
			return
		}
	}
	if len(acceptedIDs) == 0 && len(listFingerprints) == 0 && !hasGenericResourceSubscriptions {
		_ = flushSSE(taskSubscriptionComplete(id))
		return
	}

	var updates <-chan string
	if tasks != nil && len(acceptedIDs) > 0 {
		var unsubscribe func()
		updates, unsubscribe = tasks.subscribeTaskChanges(append([]string(nil), acceptedIDs...))
		defer unsubscribe()
	}
	pollInterval := 5 * time.Second
	if tasks != nil && tasks.pollInterval > pollInterval {
		pollInterval = tasks.pollInterval
	}
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	keepAlive := time.NewTicker(taskSubscriptionKeepAlive)
	defer keepAlive.Stop()
	refreshTask := func(ctx context.Context, taskID string) bool {
		index := -1
		for candidate, acceptedID := range acceptedIDs {
			if acceptedID == taskID {
				index = candidate
				break
			}
		}
		if index < 0 || tasks == nil {
			return true
		}
		value, task, err := tasks.get(ctx, taskID, identity)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				delete(current, taskID)
				tasks.releaseWatchedTaskIDs(1)
				reservedIDs--
				acceptedIDs = append(acceptedIDs[:index], acceptedIDs[index+1:]...)
			}
			return true
		}
		if auth.toolAllowed(task.ToolName, identity) != nil {
			delete(current, taskID)
			tasks.releaseWatchedTaskIDs(1)
			reservedIDs--
			acceptedIDs = append(acceptedIDs[:index], acceptedIDs[index+1:]...)
			return true
		}
		detail, fingerprint, err := taskSubscriptionDetail(value)
		if err != nil || current[taskID].fingerprint == fingerprint {
			return true
		}
		current[taskID] = snapshot{params: detail, fingerprint: fingerprint}
		if _, requested := requestedSet[taskID]; requested {
			if err := flushSSE(taskSubscriptionNotification(id, detail)); err != nil {
				return false
			}
		}
		for _, uri := range resourceURIsByTaskID[taskID] {
			if err := flushSSE(subscriptionChangeNotification(id, "notifications/resources/updated", map[string]any{"uri": uri})); err != nil {
				return false
			}
		}
		return true
	}
	refreshLists := func() bool {
		for _, key := range []string{"toolsListChanged", "resourcesListChanged", "promptsListChanged"} {
			oldFingerprint, active := listFingerprints[key]
			if !active {
				continue
			}
			fingerprint, err := subscriptionCatalogFingerprint(key, auth, identity, tasks)
			if err != nil || fingerprint == oldFingerprint {
				continue
			}
			listFingerprints[key] = fingerprint
			if err := flushSSE(subscriptionChangeNotification(id, subscriptionListChangedMethod(key), nil)); err != nil {
				return false
			}
		}
		return true
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if taskSubscriptionAuthExpired(identity) {
				return
			}
			for _, taskID := range append([]string(nil), acceptedIDs...) {
				if !refreshTask(r.Context(), taskID) {
					return
				}
			}
			if !refreshLists() {
				return
			}
			if len(current) == 0 && len(listFingerprints) == 0 && !hasGenericResourceSubscriptions {
				_ = flushSSE(taskSubscriptionComplete(id))
				return
			}
		case uri := <-resourceUpdateEvents:
			pendingResourceUpdatesMu.Lock()
			delete(pendingResourceUpdates, uri)
			pendingResourceUpdatesMu.Unlock()
			if err := flushSSE(subscriptionChangeNotification(id, "notifications/resources/updated", map[string]any{"uri": uri})); err != nil {
				return
			}
		case <-catalogUpdates:
			if !refreshLists() {
				return
			}
		case taskID := <-updates:
			if taskSubscriptionAuthExpired(identity) {
				return
			}
			if !refreshTask(r.Context(), taskID) {
				return
			}
			if len(current) == 0 && len(listFingerprints) == 0 && !hasGenericResourceSubscriptions {
				_ = flushSSE(taskSubscriptionComplete(id))
				return
			}
		case <-keepAlive.C:
			if taskSubscriptionAuthExpired(identity) {
				return
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func validTaskSubscriptionID(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch id := value.(type) {
	case string:
		return id != ""
	case float64:
		return !math.IsNaN(id) && !math.IsInf(id, 0) && math.Trunc(id) == id && math.Abs(id) <= 9_007_199_254_740_991
	default:
		return false
	}
}

func taskSubscriptionDetail(value map[string]any) (map[string]any, string, error) {
	detail := make(map[string]any, len(value))
	for key, field := range value {
		if key != "resultType" {
			detail[key] = field
		}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil, "", err
	}
	return detail, string(encoded), nil
}

func taskSubscriptionNotification(id json.RawMessage, detail map[string]any) map[string]any {
	params := make(map[string]any, len(detail)+1)
	for key, value := range detail {
		params[key] = value
	}
	params["_meta"] = map[string]any{mcpSubscriptionIDMetaKey: id}
	return map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": params}
}

func taskSubscriptionComplete(id json.RawMessage) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
		"resultType": "complete",
		"_meta": map[string]any{
			mcpSubscriptionIDMetaKey: id,
			mcpServerInfoMetaKey:     map[string]string{"name": "gregale-mcp-server", "version": "1.0.0"},
		},
	}}
}

func taskSubscriptionAuthExpired(identity *authContext) bool {
	return identity != nil && identity.expiresAt > 0 && float64(time.Now().UnixNano())/float64(time.Second) >= identity.expiresAt
}

func taskIDFromResourceURI(uri string) string {
	const prefix = "task://tasks/"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	taskID := strings.TrimPrefix(uri, prefix)
	if !taskIDPattern.MatchString(taskID) {
		return ""
	}
	return strings.ToLower(taskID)
}

func subscriptionListChangedMethod(key string) string {
	switch key {
	case "toolsListChanged":
		return "notifications/tools/list_changed"
	case "resourcesListChanged":
		return "notifications/resources/list_changed"
	default:
		return "notifications/prompts/list_changed"
	}
}

func subscriptionCatalogFingerprint(key string, auth *authenticator, identity *authContext, tasks *mcpTaskRuntime) (string, error) {
	var value any
	switch key {
	case "toolsListChanged":
		value = auth.visibleTools(identity)
	case "resourcesListChanged":
		templates := auth.visibleResourceTemplates(identity)
		if tasks != nil && auth.resourceAllowed("task://tasks/{taskId}", identity) == nil {
			templates = append(templates, taskResourceTemplate())
		}
		value = map[string]any{
			"resources":         auth.visibleResources(identity),
			"resourceTemplates": templates,
		}
	case "promptsListChanged":
		value = auth.visiblePrompts(identity)
	default:
		return "", errors.New("unknown catalog notification filter")
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func subscriptionChangeNotification(id json.RawMessage, method string, fields map[string]any) map[string]any {
	params := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		params[key] = value
	}
	params["_meta"] = map[string]any{mcpSubscriptionIDMetaKey: id}
	return map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
}
