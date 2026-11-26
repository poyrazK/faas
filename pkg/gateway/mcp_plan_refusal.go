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

// mcpPlanRefusedKey marks a request writeMCPPlanRefusal answered, so MCP
// telemetry counts it as rate limited although the status is 200.
type mcpPlanRefusedKey struct{}

// mcpInitializeKey marks an initialize request whose client declared URL
// elicitation; the answer's session id is remembered.
type mcpInitializeKey struct{}

// mcpRequest is the admitted request; a session's capabilities are looked
// up only when a refusal needs them.
type mcpRequest struct {
	id             json.RawMessage
	method         string
	urlElicitation bool
	appID, session string
}

// mcpEnvelope is what the gateway reads from an MCP JSON-RPC request.
type mcpEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name         string          `json:"name"`
		Cursor       *string         `json:"cursor"`
		Meta         json.RawMessage `json:"_meta"`
		Capabilities json.RawMessage `json:"capabilities"`
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
	// The answer echoes the id the app would read, so a body that spells a
	// field twice (Go's decoder takes the last case-insensitive match) is
	// left alone.
	var env mcpEnvelope
	if json.Unmarshal(body, &env) != nil || !mcpPolicyFieldsUnambiguous(body, "id", "method", "params") {
		return mcpEnvelope{}, false
	}
	return env, true
}

// metaClientCapabilities returns the client capabilities of a request's
// _meta; anything malformed reads as none.
func metaClientCapabilities(meta json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if len(meta) == 0 || json.Unmarshal(meta, &m) != nil {
		return nil
	}
	return m["io.modelcontextprotocol/clientCapabilities"]
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
	req := mcpRequest{id: env.ID, method: env.Method, appID: app.ID, session: r.Header.Get("Mcp-Session-Id"),
		urlElicitation: declaresURLElicitation(metaClientCapabilities(env.Params.Meta))}
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
	// limit, observed and docsURL are the problem's, for route limits.
	limit, observed *int64
	docsURL         string
}

// writeMCPPlanRefusal answers an admitted MCP JSON-RPC request the plan
// refused. False leaves the refusal to the 429 problem.
func (h *Handler) writeMCPPlanRefusal(w http.ResponseWriter, r *http.Request, refusal mcpPlanRefusal) bool {
	req, ok := r.Context().Value(mcpRequestKey{}).(mcpRequest)
	if !ok {
		return false
	}
	if !req.urlElicitation {
		req.urlElicitation = h.mcpSessions.urlElicitation(req.appID, req.session, time.Now())
	}
	text := refusal.message
	if refusal.upgradeURL != "" {
		text += " Upgrade the plan: " + refusal.upgradeURL
	}
	data := map[string]any{"code": refusal.code, "retry_after_seconds": refusal.retryAfter}
	if refusal.upgradeURL != "" {
		data["upgrade_url"] = refusal.upgradeURL
	}
	if refusal.limit != nil && refusal.observed != nil {
		data["limit"], data["observed"] = *refusal.limit, *refusal.observed
	}
	if refusal.docsURL != "" {
		data["docs_url"] = refusal.docsURL
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
	// The answer is the gateway's, not the app's, though it is a 200;
	// telemetry records it as the 429 it replaces (observedStatus).
	w.Header().Set(api.ErrorCodeHeader, refusal.code)
	*r = *r.WithContext(context.WithValue(r.Context(), mcpPlanRefusedKey{}, true))
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

// mcpSessionTTL and mcpSessionsPerApp bound the session memory
// (pkg/api/limits.go).
const (
	mcpSessionTTL     = api.MCPSessionCapabilityTTL
	mcpSessionsPerApp = api.MCPSessionCapabilitiesPerApp
)

// mcpSessionAppSweep bounds the apps one remember examines for expired
// sessions, so the lock is never held over the whole table.
const mcpSessionAppSweep = 64

// mcpSessionCapabilities remembers, per gateway process, the MCP sessions
// whose initialize declared URL elicitation. A session served by another
// node gets the plain-text refusal.
type mcpSessionCapabilities struct {
	mu   sync.Mutex
	apps map[string]map[string]time.Time
}

func (c *mcpSessionCapabilities) urlElicitation(appID, session string, now time.Time) bool {
	if session == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expires, ok := c.apps[appID][session]
	return ok && now.Before(expires)
}

// remember adds an app's session. A full app drops its expired sessions,
// then an arbitrary one; past oauthConsumerCacheMax apps, apps whose
// sessions all expired are dropped and a new app waits for room.
func (c *mcpSessionCapabilities) remember(appID, session string, now time.Time) {
	if session == "" || len(session) > 256 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apps == nil {
		c.apps = map[string]map[string]time.Time{}
	}
	sessions, ok := c.apps[appID]
	if !ok {
		if len(c.apps) >= oauthConsumerCacheMax {
			c.dropExpiredApps(now)
			if len(c.apps) >= oauthConsumerCacheMax {
				return
			}
		}
		sessions = map[string]time.Time{}
		c.apps[appID] = sessions
	}
	if len(sessions) >= mcpSessionsPerApp {
		dropExpired(sessions, now)
		for k := range sessions {
			if len(sessions) < mcpSessionsPerApp {
				break
			}
			delete(sessions, k)
		}
	}
	sessions[session] = now.Add(mcpSessionTTL)
}

// dropExpiredApps drops up to mcpSessionAppSweep apps (map order is
// random) whose sessions all expired.
func (c *mcpSessionCapabilities) dropExpiredApps(now time.Time) {
	examined := 0
	for appID, sessions := range c.apps {
		if examined++; examined > mcpSessionAppSweep {
			return
		}
		if dropExpired(sessions, now); len(sessions) == 0 {
			delete(c.apps, appID)
		}
	}
}

func dropExpired(sessions map[string]time.Time, now time.Time) {
	for k, expires := range sessions {
		if !now.Before(expires) {
			delete(sessions, k)
		}
	}
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
