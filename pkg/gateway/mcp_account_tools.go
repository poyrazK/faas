package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

// MCP account tools (ADR-20261125194510): for a caller it enrolled through
// OAuth on an app with the owner's opt-in, the gateway appends
// account_usage and account_plans to the app's tools/list answer and
// answers their calls itself from apid. The calls are unbilled and run
// before tool limits and plan admission, so a caller past its limit can
// still find the way to upgrade.

// MCPAccountToolAnswerer asks apid for an account tool's JSON answer:
// apidgrpc.OAuthConsumers. apidgrpc.ErrAccountToolsOff is a refusal.
type MCPAccountToolAnswerer interface {
	AccountTool(ctx context.Context, accountID, appID, consumerID, tool string) ([]byte, error)
}

// mcpAccountToolTimeout bounds the apid call on the request path.
const mcpAccountToolTimeout = 3 * time.Second

// mcpAccountToolsKey marks a request whose OAuth consumer's app has account
// tools on; attachOAuthConsumer sets it from the enrollment answer.
type mcpAccountToolsKey struct{}

// mcpAccountToolCallKey carries the mcpAccountRequest of an account tool
// call every matching jwt rule admitted; answerMCPAccountTool answers it.
type mcpAccountToolCallKey struct{}

// mcpAccountToolsListKey carries the JSON-RPC id of a tools/list request
// whose answer gets the account tools.
type mcpAccountToolsListKey struct{}

// WithMCPAccountTools lets MCP resource policies serve account tools. Nil
// leaves tool lists and calls to the app.
func (h *Handler) WithMCPAccountTools(a MCPAccountToolAnswerer) *Handler {
	h.mcpAccountTools, h.mcpAccountToolLimiter = a, &mcpAccountToolLimiter{}
	return h
}

func mcpAccountToolsOn(r *http.Request) bool {
	on, _ := r.Context().Value(mcpAccountToolsKey{}).(bool)
	return on
}

// mcpAccountRequest is what account tools need from a JSON-RPC request.
type mcpAccountRequest struct {
	method, tool string
	id           json.RawMessage
	cursor       bool
}

// mcpAccountRequestOf reads r's JSON-RPC envelope (readMCPEnvelope).
func (h *Handler) mcpAccountRequestOf(r *http.Request) mcpAccountRequest {
	if h.mcpAccountTools == nil {
		return mcpAccountRequest{}
	}
	envelope, ok := readMCPEnvelope(r)
	if !ok {
		return mcpAccountRequest{}
	}
	out := mcpAccountRequest{method: envelope.Method, id: envelope.ID, cursor: envelope.Params.Cursor != nil}
	if envelope.Method == "tools/call" && api.MCPAccountTool(envelope.Params.Name) {
		out.tool = envelope.Params.Name
	}
	return out
}

// mcpAccountToolCall reports whether req calls an account tool for a
// consumer whose app has account tools on.
func mcpAccountToolCall(r *http.Request, req mcpAccountRequest) bool {
	return req.tool != "" && authenticatedFrom(r.Context()).ConsumerID != "" && mcpAccountToolsOn(r)
}

// answerMCPAccountTool answers the account tool call the MCP policy
// admitted. False leaves the request to the usual path.
func (h *Handler) answerMCPAccountTool(w http.ResponseWriter, r *http.Request, app App) bool {
	req, ok := r.Context().Value(mcpAccountToolCallKey{}).(mcpAccountRequest)
	if !ok || h.mcpAccountTools == nil {
		return false
	}
	consumerID := authenticatedFrom(r.Context()).ConsumerID
	suppressFinancialUsage(r)
	if !h.mcpAccountToolLimiter.allow(consumerID, time.Now()) {
		writeMCPToolResult(w, req.id, nil, "Too many account tool calls; try again in a minute.")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), mcpAccountToolTimeout)
	defer cancel()
	out, err := h.mcpAccountTools.AccountTool(ctx, app.AccountID, app.ID, consumerID, req.tool)
	switch {
	case errors.Is(err, apidgrpc.ErrAccountToolsOff):
		writeMCPToolResult(w, req.id, nil, "Account tools are not available on this MCP server.")
	case err != nil || !json.Valid(out):
		if h.log != nil {
			h.log.Warn("mcp account tool unavailable", "err", err, "app_id", app.ID, "tool", req.tool)
		}
		writeMCPToolResult(w, req.id, nil, "Your account could not be read right now; try again shortly.")
	default:
		writeMCPToolResult(w, req.id, out, "")
	}
	return true
}

// writeMCPToolResult answers a tools/call with out as JSON text and
// structured content, or with problem as a tool error the model can read.
func writeMCPToolResult(w http.ResponseWriter, id json.RawMessage, out []byte, problem string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	result := map[string]any{"content": []map[string]string{{"type": "text", "text": string(out)}}, "structuredContent": json.RawMessage(out)}
	if problem != "" {
		result = map[string]any{"content": []map[string]string{{"type": "text", "text": problem}}, "isError": true}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// markMCPAccountToolsList arms the tools/list answer of a caller whose app
// has account tools on. Only the first page gets them, and the app is
// asked for an uncompressed answer so the gateway can read it.
func markMCPAccountToolsList(r *http.Request, req mcpAccountRequest) {
	if req.method != "tools/list" || req.cursor || len(req.id) == 0 || !mcpAccountToolsOn(r) || authenticatedFrom(r.Context()).ConsumerID == "" {
		return
	}
	r.Header.Del("Accept-Encoding")
	*r = *r.WithContext(context.WithValue(r.Context(), mcpAccountToolsListKey{}, req.id))
}

// wrapMCPAccountToolsList wraps w when r's tools/list answer gets account
// tools; finish writes what the wrapper holds back once the app answered.
func wrapMCPAccountToolsList(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	id, _ := r.Context().Value(mcpAccountToolsListKey{}).(json.RawMessage)
	if len(id) == 0 {
		return w, func() {}
	}
	aw := &mcpAccountToolsWriter{ResponseWriter: w, id: id}
	return aw, aw.finish
}

// mcpAccountToolsLimiter counts each consumer's account tool calls per UTC
// minute in this process. Once it tracks oauthConsumerCacheMax consumers in
// a minute it refuses new ones until the next, rather than forgetting the
// counts it has.
type mcpAccountToolLimiter struct {
	mu     sync.Mutex
	minute time.Time
	counts map[string]int
}

func (l *mcpAccountToolLimiter) allow(consumerID string, now time.Time) bool {
	minute := now.UTC().Truncate(time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !minute.Equal(l.minute) || l.counts == nil {
		l.minute, l.counts = minute, map[string]int{}
	}
	n, seen := l.counts[consumerID]
	if n >= api.MCPAccountToolCallsPerMinute || (!seen && len(l.counts) >= oauthConsumerCacheMax) {
		return false
	}
	l.counts[consumerID]++
	return true
}

const (
	accountToolsUndecided = iota
	accountToolsPassThrough
	accountToolsJSON
	accountToolsSSE
)

// mcpAccountToolsWriter appends the account tools to a tools/list answer:
// a JSON body (held back up to api.MCPAccountToolsListMaxBytes) or the
// matching event of a text/event-stream. Anything else passes through.
type mcpAccountToolsWriter struct {
	http.ResponseWriter
	id       json.RawMessage
	mode     int
	buf      bytes.Buffer
	injected bool
}

func (a *mcpAccountToolsWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }

func (a *mcpAccountToolsWriter) WriteHeader(code int) {
	if a.mode != accountToolsUndecided || code < http.StatusOK {
		// An informational answer (103 Early Hints) leaves the real one
		// to decide.
		a.ResponseWriter.WriteHeader(code)
		return
	}
	a.mode = accountToolsPassThrough
	h := a.Header()
	media, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	if code == http.StatusOK && (h.Get("Content-Encoding") == "" || h.Get("Content-Encoding") == "identity") {
		switch media {
		case "application/json":
			a.mode = accountToolsJSON
		case "text/event-stream":
			a.mode = accountToolsSSE
		}
	}
	if a.mode != accountToolsPassThrough {
		h.Del("Content-Length")
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *mcpAccountToolsWriter) Write(b []byte) (int, error) {
	if a.mode == accountToolsUndecided {
		a.WriteHeader(http.StatusOK)
	}
	switch a.mode {
	case accountToolsJSON:
		if a.buf.Len()+len(b) > api.MCPAccountToolsListMaxBytes {
			return a.giveUp(b)
		}
		a.buf.Write(b)
		return len(b), nil
	case accountToolsSSE:
		if a.injected {
			return a.ResponseWriter.Write(b)
		}
		a.buf.Write(b)
		if err := a.writeEvents(); err != nil {
			return 0, err
		}
		if a.buf.Len() > api.MCPAccountToolsListMaxBytes {
			return len(b), a.flushRaw()
		}
		return len(b), nil
	}
	return a.ResponseWriter.Write(b)
}

// giveUp passes what it held back and the rest through unchanged.
func (a *mcpAccountToolsWriter) giveUp(b []byte) (int, error) {
	if err := a.flushRaw(); err != nil {
		return 0, err
	}
	return a.ResponseWriter.Write(b)
}

func (a *mcpAccountToolsWriter) flushRaw() error {
	a.mode = accountToolsPassThrough
	held := a.buf.Bytes()
	a.buf = bytes.Buffer{}
	if len(held) == 0 {
		return nil
	}
	_, err := a.ResponseWriter.Write(held)
	return err
}

// writeEvents writes every complete event held back, with the account
// tools added to the answer's.
func (a *mcpAccountToolsWriter) writeEvents() error {
	for !a.injected {
		held := a.buf.Bytes()
		end, sep := sseEventEnd(held)
		if end < 0 {
			return nil
		}
		event := append([]byte(nil), held[:end+sep]...)
		a.buf.Next(end + sep)
		if rewritten, ok := a.rewriteEvent(event[:end]); ok {
			event = append(rewritten, event[end:]...)
			a.injected = true
		}
		if _, err := a.ResponseWriter.Write(event); err != nil {
			return err
		}
	}
	return a.flushRaw()
}

// sseEventEnd returns where the first complete event in b ends and the
// length of the blank line that ends it, or -1. Lines end in LF, CRLF or a
// bare CR.
func sseEventEnd(b []byte) (int, int) {
	end, sep := -1, 0
	for _, blank := range []string{"\r\n\r\n", "\n\n", "\r\r"} {
		if i := bytes.Index(b, []byte(blank)); i >= 0 && (end < 0 || i < end) {
			end, sep = i, len(blank)
		}
	}
	return end, sep
}

// rewriteEvent adds the account tools to an event whose data is the answer.
func (a *mcpAccountToolsWriter) rewriteEvent(event []byte) ([]byte, bool) {
	event = bytes.ReplaceAll(bytes.ReplaceAll(event, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	lines := bytes.Split(event, []byte("\n"))
	var data [][]byte
	var other [][]byte
	for _, line := range lines {
		if rest, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			data = append(data, bytes.TrimPrefix(rest, []byte(" ")))
			continue
		}
		other = append(other, line)
	}
	if len(data) == 0 {
		return nil, false
	}
	payload, ok := injectMCPAccountTools(bytes.Join(data, []byte("\n")), a.id)
	if !ok {
		return nil, false
	}
	out := bytes.Join(append(other, append([]byte("data: "), payload...)), []byte("\n"))
	return out, true
}

func (a *mcpAccountToolsWriter) Flush() {
	if a.mode == accountToolsJSON {
		return
	}
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish writes a held-back JSON answer, with the account tools when it is
// the tools/list result, and an unfinished event as it came. It is a no-op
// once it ran.
func (a *mcpAccountToolsWriter) finish() {
	switch a.mode {
	case accountToolsJSON:
		body := a.buf.Bytes()
		if rewritten, ok := injectMCPAccountTools(body, a.id); ok {
			body = rewritten
		}
		a.mode = accountToolsPassThrough
		_, _ = a.ResponseWriter.Write(body)
	case accountToolsSSE:
		_ = a.flushRaw()
	}
}

// injectMCPAccountTools appends the account tools to the tools of a
// tools/list result answering id, dropping app tools with their names. It
// reports false for anything else.
func injectMCPAccountTools(body []byte, id json.RawMessage) ([]byte, bool) {
	var msg map[string]json.RawMessage
	if json.Unmarshal(body, &msg) != nil || msg["result"] == nil || !sameJSONRPCID(msg["id"], id) {
		return nil, false
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(msg["result"], &result) != nil {
		return nil, false
	}
	var tools []json.RawMessage
	if json.Unmarshal(result["tools"], &tools) != nil || tools == nil {
		return nil, false
	}
	kept := tools[:0]
	for _, tool := range tools {
		var named struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(tool, &named) == nil && api.MCPAccountTool(named.Name) {
			continue
		}
		kept = append(kept, tool)
	}
	for _, def := range api.MCPAccountToolDefinitions() {
		raw, err := json.Marshal(def)
		if err != nil {
			return nil, false
		}
		kept = append(kept, raw)
	}
	var err error
	if result["tools"], err = json.Marshal(kept); err != nil {
		return nil, false
	}
	if msg["result"], err = json.Marshal(result); err != nil {
		return nil, false
	}
	out, err := json.Marshal(msg)
	return out, err == nil
}

// sameJSONRPCID compares two JSON-RPC ids, a string or a number, by value.
func sameJSONRPCID(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	switch x.(type) {
	case string, float64:
		return x == y
	}
	return false
}
