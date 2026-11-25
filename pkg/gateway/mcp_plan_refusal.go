package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// MCP plan refusals (ADR-20261125230541): an MCP client cannot read an HTTP
// 429 problem body, so a JSON-RPC request an MCP resource policy admitted
// and the consumer's plan then refuses gets a JSON-RPC answer instead:
//
//   - a client that declared URL elicitation gets error -32042 (URL
//     elicitation required) carrying the upgrade link, which it shows the
//     person;
//   - any other client gets a tools/call result with isError, or a JSON-RPC
//     error for other methods, whose text names the limit and the link.
//
// Notifications, batches and anything that is not JSON-RPC keep the 429.

// mcpURLElicitationRequired is the MCP error code asking the client to open
// a URL with the person's consent.
const mcpURLElicitationRequired = -32042

// mcpPlanRefusedCode is the JSON-RPC error code of a plan refusal for a
// client without URL elicitation (implementation-defined server error).
const mcpPlanRefusedCode = -32000

// mcpEnvelopeMaxBytes bounds the body the gateway reads to classify an MCP
// request; mcpPolicyRequest applies the same bound.
const mcpEnvelopeMaxBytes = api.MCPPolicyMaxRequestBytes

// mcpRequestKey carries the JSON-RPC request an MCP resource policy admitted.
type mcpRequestKey struct{}

// mcpInitializeKey marks an initialize request whose client declared URL
// elicitation; the answer's session id is remembered.
type mcpInitializeKey struct{}

type mcpRequest struct {
	id             json.RawMessage
	method         string
	urlElicitation bool
}

// mcpEnvelope is what the gateway reads from an MCP JSON-RPC request.
type mcpEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name         string                     `json:"name"`
		Cursor       *string                    `json:"cursor"`
		Meta         map[string]json.RawMessage `json:"_meta"`
		Capabilities json.RawMessage            `json:"capabilities"`
	} `json:"params"`
}

// readMCPEnvelope reads r's JSON-RPC envelope and restores the body. False
// for a missing, oversized or non-object body (a batch, say).
func readMCPEnvelope(r *http.Request) (mcpEnvelope, bool) {
	if r.Method != http.MethodPost || r.Body == nil {
		return mcpEnvelope{}, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, mcpEnvelopeMaxBytes+1))
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > mcpEnvelopeMaxBytes {
		return mcpEnvelope{}, false
	}
	var env mcpEnvelope
	if json.Unmarshal(body, &env) != nil {
		return mcpEnvelope{}, false
	}
	return env, true
}

// declaresURLElicitation reports whether client capabilities include
// elicitation.url.
func declaresURLElicitation(capabilities json.RawMessage) bool {
	var c struct {
		Elicitation map[string]json.RawMessage `json:"elicitation"`
	}
	if len(capabilities) == 0 || json.Unmarshal(capabilities, &c) != nil {
		return false
	}
	v, ok := c.Elicitation["url"]
	return ok && string(v) != "null" && string(v) != "false"
}

// markMCPRequest records the admitted JSON-RPC request for a refusal later
// on the path. URL elicitation comes from the request's own client
// capabilities (protocol 2026-07-28) or from the initialize of the
// request's session.
func (h *Handler) markMCPRequest(r *http.Request, app App) {
	env, ok := readMCPEnvelope(r)
	if !ok || len(env.ID) == 0 || env.Method == "" {
		return
	}
	req := mcpRequest{id: env.ID, method: env.Method,
		urlElicitation: declaresURLElicitation(env.Params.Meta["io.modelcontextprotocol/clientCapabilities"]) ||
			h.mcpSessions.urlElicitation(app.ID, r.Header.Get("Mcp-Session-Id"), time.Now())}
	ctx := context.WithValue(r.Context(), mcpRequestKey{}, req)
	if env.Method == "initialize" && declaresURLElicitation(env.Params.Capabilities) {
		ctx = context.WithValue(ctx, mcpInitializeKey{}, app.ID)
	}
	*r = *r.WithContext(ctx)
}

// mcpPlanRefusal is a consumer plan refusal in MCP terms.
type mcpPlanRefusal struct {
	code       string
	message    string
	upgradeURL string
	retryAfter int64
}

// writeMCPPlanRefusal answers an admitted MCP JSON-RPC request the plan
// refused. False leaves the refusal to the 429 problem.
func writeMCPPlanRefusal(w http.ResponseWriter, r *http.Request, refusal mcpPlanRefusal) bool {
	req, ok := r.Context().Value(mcpRequestKey{}).(mcpRequest)
	if !ok {
		return false
	}
	text := refusal.message
	if refusal.upgradeURL != "" {
		text += " Upgrade the plan: " + refusal.upgradeURL
	}
	data := map[string]any{"code": refusal.code, "retry_after_seconds": refusal.retryAfter}
	if refusal.upgradeURL != "" {
		data["upgrade_url"] = refusal.upgradeURL
	}
	answer := map[string]any{"jsonrpc": "2.0", "id": req.id}
	switch {
	case req.urlElicitation && refusal.upgradeURL != "":
		data["elicitations"] = []map[string]string{{"mode": "url", "elicitationId": uuid.NewString(),
			"url": refusal.upgradeURL, "message": refusal.message + " Open this page to upgrade the plan."}}
		answer["error"] = map[string]any{"code": mcpURLElicitationRequired, "message": text, "data": data}
	case req.method == "tools/call":
		answer["result"] = map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": true}
	default:
		answer["error"] = map[string]any{"code": mcpPlanRefusedCode, "message": text, "data": data}
	}
	body, err := json.Marshal(answer)
	if err != nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// The answer is the gateway's, not the app's, though it is a 200.
	w.Header().Set(api.ErrorCodeHeader, refusal.code)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return true
}

// mcpPlanRefusalMessage says what the plan refused, and when a per-minute
// limit clears.
func mcpPlanRefusalMessage(decision ConsumerPlanDecision, route string) string {
	msg := "Plan limit exceeded: " + consumerPlanDenialDetail(decision, route) + "."
	if decision.Scope == "minute" || decision.Scope == "route_minute" {
		msg += " It clears in " + strconv.FormatInt(max(1, decision.RetryAfterSeconds), 10) + " s."
	}
	return msg
}

// mcpSessionTTL is how long the gateway remembers that a session's client
// declared URL elicitation.
const mcpSessionTTL = 24 * time.Hour

// mcpSessionCapabilities remembers, per gateway process, the MCP sessions
// whose initialize declared URL elicitation. A session served by another
// node gets the plain-text refusal.
type mcpSessionCapabilities struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func (c *mcpSessionCapabilities) urlElicitation(appID, session string, now time.Time) bool {
	if session == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expires, ok := c.entries[appID+"\x00"+session]
	return ok && now.Before(expires)
}

func (c *mcpSessionCapabilities) remember(appID, session string, now time.Time) {
	if session == "" || len(session) > 256 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]time.Time{}
	}
	if len(c.entries) >= oauthConsumerCacheMax {
		for k, expires := range c.entries {
			if !now.Before(expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= oauthConsumerCacheMax {
			return
		}
	}
	c.entries[appID+"\x00"+session] = now.Add(mcpSessionTTL)
}

// wrapMCPInitialize remembers the session id of an initialize answer whose
// client declared URL elicitation.
func (h *Handler) wrapMCPInitialize(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	appID, _ := r.Context().Value(mcpInitializeKey{}).(string)
	if appID == "" {
		return w
	}
	return &mcpInitializeWriter{ResponseWriter: w, appID: appID, sessions: &h.mcpSessions}
}

type mcpInitializeWriter struct {
	http.ResponseWriter
	appID    string
	sessions *mcpSessionCapabilities
	seen     bool
}

func (m *mcpInitializeWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

func (m *mcpInitializeWriter) WriteHeader(code int) {
	if !m.seen && code >= http.StatusOK {
		m.seen = true
		if code == http.StatusOK {
			m.sessions.remember(m.appID, m.Header().Get("Mcp-Session-Id"), time.Now())
		}
	}
	m.ResponseWriter.WriteHeader(code)
}

func (m *mcpInitializeWriter) Write(b []byte) (int, error) {
	if !m.seen {
		m.WriteHeader(http.StatusOK)
	}
	return m.ResponseWriter.Write(b)
}

func (m *mcpInitializeWriter) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
